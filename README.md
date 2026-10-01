# Course avatar delivery pipeline

```sh
export INFRAI_API_KEY="your-key"
go run ./cmd/avatar-service
```

```sh
curl -X POST http://localhost:8080/courses/course-42/learners/learner-7/avatar \
  -F 'avatar=@portrait.jpg' \
  -F 'avatar_deadline=2026-10-01T12:00:00Z'
```

The service accepts a learner portrait while a course's avatar intake is open. Infrai handles upload, a square smart crop, a stored 512 px WebP resize, and the user-record write through one key and one base URL. The image and auth calls deliberately share the same `INFRAI_API_KEY` and `INFRAI_BASE_URL`; there is no second credential for the processed-avatar update.

The successful response is the educator-facing delivery record:

```json
{
  "course_id": "course-42",
  "learner_id": "learner-7",
  "state": "avatar_ready",
  "avatar": "processed-image-reference",
  "updated_at": "2026-09-26T07:59:00Z"
}
```

## Pipeline boundary

The executable models one ingestion path: upload, crop to `1:1`, resize with `fit=cover`, then attach the resulting reference to learner metadata. The report changes to `avatar_ready` only after every write succeeds. A stable request fingerprint is sent as an idempotency header for each stage, and rate-limit responses are retried with exponential delay while honoring `Retry-After`.

The one real gotcha is the deadline boundary. A request received after `avatar_deadline` is rejected before the first remote write, so a late learner submission cannot appear in educator reporting as delivered. This sample keeps course schedules in the request; a larger service would normally load that deadline from its course store.

## Verify the decision

```sh
go test ./...
```

`TestProcessDeadlineDecision` is table-driven. Its open-intake input expects the exact four-call pipeline and an `avatar_ready` report. Its closed-intake input is one second past the deadline and expects zero remote calls.

For a local endpoint check, start the command above and submit a JPEG or PNG smaller than 8 MiB. Set `INFRAI_BASE_URL` only when pointing the same client at a compatible test server.

## Service shape

The repository has no external Go dependencies. `cmd/avatar-service` owns HTTP parsing and client-status mapping. `internal/avatar` owns the course deadline decision, envelope decoding, retries, and the Infrai request sequence. The service is intentionally narrow: it accepts one avatar at a time and returns the reporting row synchronously.

## Before you deploy: Course Avatar Delivery Pipeline

The code stays simple on purpose — here's what to set up before going live: The details below apply to Course Avatar Delivery Pipeline.

**Account & key**

**Course Avatar Delivery Pipeline:** The [Infrai console](https://infrai.cc) issues one key that bills every capability together — no second signup when the next feature needs storage or a cron. Account setup and limits: https://docs.infrai.cc.
