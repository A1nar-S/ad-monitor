#!/usr/bin/env bash
# Runs the app on this machine's k3s (see deploy/README.md). From the repo root:
#
#   bash deploy/k3s.sh images        build the images with Docker and import them into k3s
#   bash deploy/k3s.sh secrets       load .env into the cluster (ENV_FILE=... for another file)
#   bash deploy/k3s.sh tunnel-token  store the Cloudflare tunnel token (asked for, not echoed)
#   bash deploy/k3s.sh flux          install Flux: from then on the cluster follows main (deploy/flux)
#   bash deploy/k3s.sh apply         apply deploy/k8s (app, then monitoring) and restart the services
#                                    (without Flux only: Flux would undo it)
#   bash deploy/k3s.sh status        pods, and the tunnel's last log lines
#   bash deploy/k3s.sh grafana       Grafana on http://127.0.0.1:3000 while this runs
#
# KUBECTL and CTR can be overridden, e.g. CTR="docker exec -i k3s ctr" for k3s in a container.
set -euo pipefail

NS=admon
MON=monitoring
SERVICES=(scraper ingest matcher notifier bot api admin)
KUBECTL=${KUBECTL:-kubectl}
CTR=${CTR:-sudo k3s ctr}
cd "$(dirname "$0")/.."

ensure_namespace() {
	$KUBECTL create namespace "$1" --dry-run=client -o yaml | $KUBECTL apply -f - >/dev/null
}

# Secret $2 in namespace $1 from KEY=value lines on stdin. Server-side apply:
# a client-side one keeps a plain copy in an annotation.
put_secret() {
	$KUBECTL -n "$1" create secret generic "$2" --from-env-file=/dev/stdin --dry-run=client -o yaml |
		$KUBECTL apply --server-side --force-conflicts -f -
}

# Pods read the Secrets at start; restart the services so they see a change.
restart_apps() {
	if [ -n "$($KUBECTL -n "$NS" get deploy -l tier=app -o name 2>/dev/null)" ]; then
		$KUBECTL -n "$NS" rollout restart deploy -l tier=app
	fi
}

case "${1:-}" in
images)
	for s in "${SERVICES[@]}"; do
		docker build --build-arg CMD="$s" -t "admon/$s:local" .
	done
	# k3s has its own containerd and does not see Docker's images.
	images=()
	for s in "${SERVICES[@]}"; do images+=("admon/$s:local"); done
	docker save "${images[@]}" | $CTR -n k8s.io images import -
	;;
secrets)
	env_file=${ENV_FILE:-.env}
	test -f "$env_file" || { echo "missing $env_file (make env)"; exit 1; }
	val() { sed -n "s/^$1=//p" "$env_file" | tail -n 1; }
	for k in DB_USER DB_PASSWORD DB_NAME MONITOR_DB_PASSWORD GRAFANA_ADMIN_PASSWORD; do
		test -n "$(val "$k")" || { echo "$k is empty or missing in $env_file (make env generates it)"; exit 1; }
	done
	case "$(val PUBLIC_URL)" in
	https://*) ;;
	*) echo "warning: PUBLIC_URL in $env_file is not https://; login links will not reach the tunnel" >&2 ;;
	esac
	ensure_namespace "$NS"
	ensure_namespace "$MON"
	# The services get .env without the monitoring passwords.
	grep -vE '^(GRAFANA_|MONITOR_)' "$env_file" | put_secret "$NS" admon-env
	printf 'MONITOR_DB_PASSWORD=%s\n' "$(val MONITOR_DB_PASSWORD)" | put_secret "$NS" admon-monitor
	# Grafana refuses an empty contact point URL: without a webhook, alerts show in its UI only.
	hook=$(val GRAFANA_DISCORD_WEBHOOK_URL)
	printf 'admin-user=admin\nadmin-password=%s\nGRAFANA_DISCORD_WEBHOOK_URL=%s\n' \
		"$(val GRAFANA_ADMIN_PASSWORD)" "${hook:-http://127.0.0.1:9/discord-webhook-not-configured}" |
		put_secret "$MON" grafana
	# Flux (or the next apply) reruns it, which sets the new MONITOR_DB_PASSWORD.
	$KUBECTL -n "$NS" delete job pg-monitor-init --ignore-not-found
	restart_apps
	$KUBECTL -n "$MON" rollout restart deploy/grafana 2>/dev/null || true
	;;
tunnel-token)
	read -rsp "Cloudflare tunnel token: " token
	echo
	test -n "$token" || { echo "no token given"; exit 1; }
	ensure_namespace "$NS"
	# Through stdin, not argv: other users can read a process's arguments.
	printf 'token=%s\n' "$token" | put_secret "$NS" cloudflared
	$KUBECTL -n "$NS" rollout restart deploy/cloudflared 2>/dev/null || true
	;;
apply)
	# A Job's spec is immutable; both are idempotent, so just run them again.
	$KUBECTL -n "$NS" delete job kafka-topics pg-monitor-init --ignore-not-found
	$KUBECTL apply -k deploy/k8s
	# Monitoring: the Helm install brings the PodMonitor type; wait for it.
	$KUBECTL apply --server-side --force-conflicts -k deploy/k8s/monitoring/stack
	echo "waiting for the Prometheus Operator's CRDs (first install: a few minutes)"
	for _ in $(seq 120); do
		$KUBECTL get crd podmonitors.monitoring.coreos.com >/dev/null 2>&1 && break
		sleep 5
	done
	$KUBECTL wait --for condition=established --timeout=60s crd/podmonitors.monitoring.coreos.com
	$KUBECTL kustomize --load-restrictor LoadRestrictionsNone deploy/k8s/monitoring |
		$KUBECTL apply --server-side --force-conflicts -f -
	# Grafana reads the provisioned alert rules at start only.
	$KUBECTL -n "$MON" rollout restart deploy/grafana 2>/dev/null || true
	# The images keep their :local tag, so a rebuild needs a restart to be used.
	restart_apps
	deploys=$($KUBECTL -n "$NS" get deploy -l tier=app -o name)
	for d in $deploys; do
		$KUBECTL -n "$NS" rollout status "$d" --timeout=10m
	done
	;;
flux)
	command -v flux >/dev/null || { echo "missing the flux CLI (sudo pacman -S fluxcd)"; exit 1; }
	# Only what a git -> cluster sync needs; no Helm, notification or image controllers.
	flux install --components=source-controller,kustomize-controller
	$KUBECTL apply -f deploy/flux/sync.yaml
	echo "Flux now applies main; watch it with: flux get kustomizations --watch"
	;;
status)
	$KUBECTL -n "$NS" get pods
	echo
	$KUBECTL -n "$MON" get pods
	echo
	$KUBECTL -n "$NS" logs deploy/cloudflared --tail=5 2>/dev/null || true
	if command -v flux >/dev/null && $KUBECTL get ns flux-system >/dev/null 2>&1; then
		echo
		flux get kustomizations
	fi
	;;
grafana)
	echo "Grafana: http://127.0.0.1:3000 (user admin, GRAFANA_ADMIN_PASSWORD); Ctrl+C stops"
	$KUBECTL -n "$MON" port-forward svc/grafana 3000:80
	;;
*)
	sed -n '2,13p' "$0"
	exit 1
	;;
esac
