# Hochhuus leiht.

The lending list for our building: residents list tools, appliances and
anything else they are happy to lend, and neighbours find it and get in touch.

It started as a neighbour's single-file offline page (kept as-is under
[`web/public/offline/`](web/public/offline/) and still offered as a download).
This is the shared online edition: a Go server with Postgres for the list and
an S3 bucket for item photos, and a Vue frontend that keeps the original look,
the German/English texts and the backup format, so a backup moves freely
between the two editions.

## How access works

There are no accounts.

- **House code** (`HOUSE_CODE`): whoever enters it can see the list and add
  items. Hand it out in the building.
- **Own listings**: the browser that created a listing can edit and remove it.
- **Admin code** (`ADMIN_CODE`): may edit and remove every listing, and import
  a JSON backup from the offline edition (existing ids are skipped).

Changing either code signs everyone out. With no `HOUSE_CODE` the list is open
to anyone, which is only meant for local development.

## Configuration

| Variable | Required | What it is |
|---|---|---|
| `DATABASE_URL` | yes | Postgres connection string |
| `HOUSE_CODE` | in production | The code residents enter |
| `ADMIN_CODE` | no | The code that may change every listing and import backups |
| `SESSION_SECRET` | in production | Signs the cookies. Without it, sessions end on every restart |
| `S3_ENDPOINT`, `S3_BUCKET`, `S3_REGION`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY` | for photos | The bucket. Without them, photo upload is hidden |
| `S3_FORCE_PATH_STYLE` | for MinIO | `true` to address the bucket in the path |
| `S3_CA_FILE` | no | A CA certificate (PEM) to trust for the bucket |
| `PORT` | no | Defaults to 8080 |

Photos are scaled down to 1600 px in the browser and again on the server,
and re-encoded as JPEG, which also drops camera metadata such as location.
They are served through the app, so the bucket stays private.

## Running locally

```sh
cd web && npm ci && npm run build && cd ..
DATABASE_URL=postgres://localhost/hochhuus HOUSE_CODE=test go run .
```

For frontend work, run the Go server and `npm run dev` in `web/`; Vite
proxies `/api` to `localhost:8080`.

`hochhuus migrate` brings the schema up to date and exits; `hochhuus` (or
`hochhuus serve`) also migrates on start.

## Deploying on Kitchen

`kitchen.json` declares the build (the `Dockerfile`), the health check, and a
`migrate` task that runs before each release takes traffic. The rest is set
up once for the project (here `hochhuus`):

```sh
kitchen link --project hochhuus

# Postgres and a bucket
kitchen api POST /claims --data '{"name": "hochhuus-db", "project": "hochhuus", "connection": "<postgres connection>", "type": "postgres"}'
kitchen api POST /claims --data '{"name": "hochhuus-photos", "project": "hochhuus", "connection": "<objectStore connection>", "type": "objectStore", "objectStore": {"size": "5Gi"}}'

kitchen env set --from-claim DATABASE_URL=hochhuus-db:url
kitchen env set --from-claim S3_ENDPOINT=hochhuus-photos:endpoint
kitchen env set --from-claim S3_BUCKET=hochhuus-photos:bucket
kitchen env set --from-claim S3_REGION=hochhuus-photos:region
kitchen env set --from-claim S3_ACCESS_KEY_ID=hochhuus-photos:accessKeyId
kitchen env set --from-claim S3_SECRET_ACCESS_KEY=hochhuus-photos:secretAccessKey
kitchen env set --from-claim S3_FORCE_PATH_STYLE=hochhuus-photos:forcePathStyle
kitchen env set --from-claim S3_CA_FILE=hochhuus-photos:caCertFile   # bundled store only

# Codes and the cookie key
kitchen secret set house-code --value-stdin < house-code.txt
kitchen secret set admin-code --value-stdin < admin-code.txt
openssl rand -hex 32 | kitchen secret set session-secret --value-stdin
kitchen env set --from-secret HOUSE_CODE=kitchen-project-secrets:house-code
kitchen env set --from-secret ADMIN_CODE=kitchen-project-secrets:admin-code
kitchen env set --from-secret SESSION_SECRET=kitchen-project-secrets:session-secret

kitchen deploy
```
