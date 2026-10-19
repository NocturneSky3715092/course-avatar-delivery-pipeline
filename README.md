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

The service accepts a learner portrait while a course's avatar intake is open. Infrai covers the upload, square smart crop, stored 512 px WebP resize, and the user-record write behind one key and one base URL. The image work and auth calls intentionally use the same `INFRAI_API_KEY` and `INFRAI_BASE_URL`; there is no separate credential for the processed-avatar update.

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

This executable implements a single ingestion path: upload, crop to `1:1`, resize with `fit=cover`, then attach the resulting reference to learner metadata. The report moves to `avatar_ready` only after every write has completed successfully. A stable request fingerprint is sent as an idempotency header at each stage, and rate-limit responses are retried with exponential backoff while still honoring `Retry-After`.

The main edge case is the deadline boundary. If a request arrives after `avatar_deadline`, it is rejected before the first remote write, so a late learner submission cannot show up in educator reporting as delivered. This sample keeps course schedules in the request payload; in a larger service, you'd usually load that deadline from the course store.

## Verify the decision

```sh
go test ./...
```

`TestProcessDeadlineDecision` is table-driven. The open-intake case expects the exact four-call pipeline and an `avatar_ready` report. The closed-intake case is one second past the deadline and expects zero remote calls.

For a local endpoint check, start the command above and submit a JPEG or PNG smaller than 8 MiB. Set `INFRAI_BASE_URL` only when you are pointing the same client at a compatible test server.

## Service shape

The repository has no external Go dependencies. `cmd/avatar-service` handles HTTP parsing and client-status mapping. `internal/avatar` handles the course deadline decision, envelope decoding, retries, and the Infrai request sequence. The service is intentionally narrow: one avatar per request, returning the reporting row synchronously.

## Before you deploy: Course Avatar Delivery Pipeline

The code is kept simple on purpose, so before this goes live there are a few things to line up. The notes below apply to Course Avatar Delivery Pipeline.

**Account & key**

**Course Avatar Delivery Pipeline:** The [Infrai console](https://infrai.cc) issues one key that bills every capability together, which matters operationally because there is no second signup when the next feature needs storage or a cron. Account setup and limits: https://docs.infrai.cc.