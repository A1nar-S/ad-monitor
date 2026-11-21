#!/usr/bin/env bash
# Runs the app on this machine's k3s (see deploy/README.md). From the repo root:
#
#   bash deploy/k3s.sh images        build the images with Docker and import them into k3s
#   bash deploy/k3s.sh secrets       load .env into the cluster (ENV_FILE=... for another file)
#   bash deploy/k3s.sh tunnel-token  store the Cloudflare tunnel token (asked for, not echoed)
#   bash deploy/k3s.sh apply         apply deploy/k8s and restart the services onto the new images
#   bash deploy/k3s.sh status        pods, and the tunnel's last log lines
#
# KUBECTL and CTR can be overridden, e.g. CTR="docker exec -i k3s ctr" for k3s in a container.
set -euo pipefail

NS=admon
SERVICES=(scraper ingest matcher notifier bot api admin)
KUBECTL=${KUBECTL:-kubectl}
CTR=${CTR:-sudo k3s ctr}
cd "$(dirname "$0")/.."

ensure_namespace() {
	$KUBECTL create namespace "$NS" --dry-run=client -o yaml | $KUBECTL apply -f - >/dev/null
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
	grep -q '^PUBLIC_URL=https://' "$env_file" ||
		echo "warning: PUBLIC_URL in $env_file is not https://; login links will not reach the tunnel and the cookie is not Secure" >&2
	for k in DB_USER DB_PASSWORD DB_NAME; do
		grep -q "^$k=." "$env_file" || { echo "$k is empty or missing in $env_file"; exit 1; }
	done
	ensure_namespace
	# Grafana and monitoring passwords are not for the services. Server-side
	# apply: a client-side one keeps a plain copy in an annotation.
	grep -vE '^(GRAFANA_|MONITOR_)' "$env_file" |
		$KUBECTL -n "$NS" create secret generic admon-env --from-env-file=/dev/stdin --dry-run=client -o yaml |
		$KUBECTL apply --server-side --force-conflicts -f -
	restart_apps
	;;
tunnel-token)
	read -rsp "Cloudflare tunnel token: " token
	echo
	test -n "$token" || { echo "no token given"; exit 1; }
	ensure_namespace
	# Through stdin, not argv: other users can read a process's arguments.
	printf %s "$token" | $KUBECTL -n "$NS" create secret generic cloudflared --from-file=token=/dev/stdin \
		--dry-run=client -o yaml | $KUBECTL apply --server-side --force-conflicts -f -
	$KUBECTL -n "$NS" rollout restart deploy/cloudflared 2>/dev/null || true
	;;
apply)
	# A Job's spec is immutable; it is idempotent, so just run it again.
	$KUBECTL -n "$NS" delete job kafka-topics --ignore-not-found
	$KUBECTL apply -k deploy/k8s
	# The images keep their :local tag, so a rebuild needs a restart to be used.
	restart_apps
	deploys=$($KUBECTL -n "$NS" get deploy -l tier=app -o name)
	for d in $deploys; do
		$KUBECTL -n "$NS" rollout status "$d" --timeout=10m
	done
	;;
status)
	$KUBECTL -n "$NS" get pods -o wide
	echo
	$KUBECTL -n "$NS" logs deploy/cloudflared --tail=5 2>/dev/null || true
	;;
*)
	sed -n '2,10p' "$0"
	exit 1
	;;
esac
