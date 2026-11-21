# Deploy: k3s + Cloudflare Tunnel

Everything runs on one k3s node. Only the dashboard is public, through a tunnel that dials out: no static IP,
no open ports. Monitoring is a trimmed kube-prometheus-stack (Prometheus + Grafana, no Alertmanager) with the
compose dashboards and alert rules; Grafana stays private.

## Machine (Arch)

```sh
sudo pacman -S --needed docker docker-buildx git make openssl fluxcd
sudo systemctl enable --now docker && sudo usermod -aG docker "$USER"   # re-login
sudo mkdir -p /etc/rancher/k3s && sudo cp deploy/k3s-config.yaml /etc/rancher/k3s/config.yaml
curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=v1.37.2+k3s1 sh -
mkdir -p ~/.kube && sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config && sudo chown "$USER": ~/.kube/config
```

Laptop: set `HandleLidSwitch=ignore` in `/etc/systemd/logind.conf` and mask `sleep.target suspend.target`.
On networks you don't trust, firewall inbound traffic (k3s listens on 6443 and 10250; pod ranges 10.42/16, 10.43/16
must stay allowed).

## Cloudflare

1. Add the domain (Free plan), switch its nameservers at the registrar.
2. Zero Trust → Networks → Tunnels → create a Cloudflared tunnel; copy the token after `--token`.
3. Tunnel → Published application routes: subdomain of your choice → `http://api:8080`. Nothing else.
4. Domain: Always Use HTTPS on; WAF rate limit on `/api/login` (10 req / 10 s per IP → Block); Bot Fight Mode on.

## App

```sh
make env                         # fill in the Discord values, PUBLIC_URL=https://<tunnel hostname>,
                                 # optionally GRAFANA_DISCORD_WEBHOOK_URL for alerts
bash deploy/k3s.sh secrets       # .env -> Secrets (rerun after editing .env)
bash deploy/k3s.sh tunnel-token
bash deploy/k3s.sh flux          # from now on the cluster follows main
bash deploy/k3s.sh status
bash deploy/k3s.sh grafana       # http://127.0.0.1:3000
```

## Releasing (GitOps)

```sh
git tag v1.2.3 && git push origin v1.2.3
```

The Release workflow builds the seven images, pushes them to `ghcr.io/a1nar-s/advertisiment-monitoring/<service>`
and commits `Deploy v1.2.3` to main, pinning each image by digest in `deploy/k8s/kustomization.yaml`. Flux
(`deploy/flux/sync.yaml`) polls main every minute and applies `deploy/k8s`, then the monitoring stack and
`deploy/k8s/monitoring` once its CRDs exist. Whatever is on main is what runs: a manifest change deploys on push,
a rollback is `git revert` of the deploy commit, and anything changed by hand in the cluster is put back.

- First release only: GHCR makes new packages private. Set each of the seven to public (GitHub → Packages →
  package → Settings → Change visibility), or the node cannot pull them.
- `flux get kustomizations` shows what was applied and why something failed; `flux reconcile kustomization admon
  --with-source` applies main right away.
- Until the first release the images list says `:local`: `bash deploy/k3s.sh images` imports those, as before.
- Secrets are not in git: `secrets` and `tunnel-token` stay manual. Grafana rereads alert rules only at start:
  `kubectl -n monitoring rollout restart deploy/grafana` after changing them.
- Without Flux, `bash deploy/k3s.sh images && bash deploy/k3s.sh apply` still deploys the working tree.

Grafana runs on the laptop: watching it from elsewhere needs `ssh -L 3000:127.0.0.1:3000` or Tailscale, not
the tunnel. Monitoring on the same machine cannot tell you the machine is down; a free external heartbeat
(e.g. healthchecks.io) covers that.

Don't run `make up` with the same Discord token at the same time: both bots would answer.

## Ops

```sh
kubectl -n admon logs -f deploy/scraper
kubectl -n admon create job --from=cronjob/purge purge-now      # also nightly
kubectl -n admon exec postgres-0 -- sh -c 'pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' | gzip > backup.sql.gz
```

Restore: `gunzip -c backup.sql.gz | kubectl -n admon exec -i postgres-0 -- sh -c 'psql -U "$POSTGRES_USER" "$POSTGRES_DB"'`
into an empty database, with the services scaled to 0.
