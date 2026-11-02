# Deploy Redline

Redline needs a Go server and Neo4j. There are two practical ways to give CI a stable URL.

## Option A — Render Free + AuraDB Free

This is the cheapest no-card setup.

1. Create a **Neo4j AuraDB Free** instance at <https://console.neo4j.io>.
2. Save the connection URI (`neo4j+s://…`), username (`neo4j`) and password.
3. Open <https://render.com>, choose **New → Blueprint**, and point it at this repository. `render.yaml` defines the web service.
4. Fill the secret environment variables:
   - `REDLINE_API_KEY` — a long random key
   - `REDLINE_USERS` — optional, `ega:key-ega,budi:key-budi`
   - `NEO4J_URI` — the AuraDB URI
   - `NEO4J_PASSWORD` — the AuraDB password
5. Deploy. The URL is `https://<service>.onrender.com`; open `/ui/`.
6. Put the same URL and key into the Playwright project:
   ```bash
   REDLINE_URL=https://<service>.onrender.com
   REDLINE_API_KEY=<key>
   ```

### Keep the free service warm

Render free services sleep after 15 minutes. The Playwright reporter has a 15-second timeout, so a sleeping service can lose the first CI report.

Use a free uptime monitor (UptimeRobot or cron-job.org) to send `GET /healthz` every 5–10 minutes. One always-on free service consumes about 730 instance hours per month; the free quota is 750.

### Limitations

- Free Render has **no persistent disk**. Traces and screenshots uploaded by the reporter live under `/tmp/redline-artifacts` and are lost on redeploy or restart. Add a paid disk and set `ARTIFACT_DIR=/data/artifacts` if artifacts must survive.
- AuraDB Free pauses after three days without writes. Resume it from the console before a demo.
- Render free is not a production SLA. It can restart at any time.

## Option B — VPS + Docker Compose

Use any small VPS with Docker and a reverse proxy.

```bash
git clone https://github.com/ega-septian/redline.git
cd redline
cp .env.example .env
```

Set at least:

```bash
REDLINE_API_KEY=<long-random-key>
REDLINE_USERS=ega:<key-ega>
NEO4J_PASSWORD=<strong-password>
TIMEZONE=Asia/Jakarta
```

Then:

```bash
docker compose --profile full up -d --build
```

The compose file keeps Neo4j on loopback, stores graph data in the `redline-neo4j` volume and artifacts in `redline-artifacts`, and restarts both services.

Expose port `8787` through Caddy/nginx/Traefik or a Cloudflare Tunnel. Example Caddy:

```caddyfile
redline.example.com {
  reverse_proxy 127.0.0.1:8787
}
```

Use a real domain and HTTPS; do not expose Neo4j. For a quick tunnel without a domain:

```bash
cloudflared tunnel --url http://127.0.0.1:8787
```

## CI configuration

In the Playwright project:

```bash
REDLINE_URL=https://redline.example.com
REDLINE_API_KEY=<key>
```

The reporter uploads trace and screenshot files automatically for failed tests. Disable that with:

```bash
REDLINE_ARTIFACTS=0
```

Limit each artifact with `MAX_ARTIFACT_MB` (default 25). Uploaded artifacts are removed together with the
run detail after `RETENTION_DAYS` (default 30; `0` keeps them forever).

## Health and backups

- Liveness: `GET /healthz`
- Readiness (includes Neo4j): `GET /readyz`
- Neo4j backup: `./scripts/backup.sh`; restore with `./scripts/restore.sh <dump>`.
- Back up the artifact directory as well if you rely on traces.

## Security checklist

- [ ] `REDLINE_API_KEY` and `NEO4J_PASSWORD` are strong and not committed.
- [ ] Neo4j is not publicly reachable.
- [ ] The UI is behind HTTPS.
- [ ] Artifact files are only served through `/api/artifacts/{id}` with authentication.
- [ ] `MAX_ARTIFACT_MB` and `MAX_REPORT_MB` fit the server disk.
