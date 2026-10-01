package avatar

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestProcessDeadlineDecision(t *testing.T) {
	deadline := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		now       time.Time
		wantState string
		wantErr   error
		wantCalls []string
	}{
		{name: "open intake runs pipeline", now: deadline.Add(-time.Minute), wantState: "avatar_ready", wantCalls: []string{"POST /v1/image/upload", "POST /v1/image/smart_crop", "POST /v1/image/resize", "PATCH /v1/auth/user/update/learner-7"}},
		{name: "closed intake makes no writes", now: deadline.Add(time.Second), wantErr: ErrDeadlinePassed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Idempotency-Key") == "" {
					t.Fatalf("missing request credentials or idempotency key")
				}
				if r.URL.Path == "/v1/auth/user/update/learner-7" {
					body, _ := io.ReadAll(r.Body)
					var userPayload struct {
						UserID   string            `json:"user_id"`
						Metadata map[string]string `json:"metadata"`
					}
					if err := json.Unmarshal(body, &userPayload); err != nil || userPayload.UserID != "learner-7" || userPayload.Metadata["avatar"] != "asset-3" {
						t.Fatalf("unexpected user update: %s", body)
					}
					w.Write([]byte(`{"ok":true,"data":{}}`))
					return
				}
				if r.URL.Path == "/v1/image/upload" {
					var upload map[string]string
					if err := json.NewDecoder(r.Body).Decode(&upload); err != nil || upload["file"] == "" || upload["filename"] != "portrait.jpg" {
						t.Fatalf("unexpected upload request: %v", upload)
					}
				}
				ref := map[string]string{"/v1/image/upload": "asset-1", "/v1/image/smart_crop": "asset-2", "/v1/image/resize": "asset-3"}[r.URL.Path]
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]string{"image_id": ref}})
			}))
			defer server.Close()

			client := NewClient(server.URL, "test-key")
			report, err := client.Process(context.Background(), ProcessInput{
				Delivery: CourseDelivery{CourseID: "course-42", LearnerID: "learner-7", AvatarDeadline: deadline},
				Filename: "portrait.jpg", Image: []byte("image-bytes"), Now: tt.now,
			})
			if !errorsIs(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if report.State != tt.wantState {
				t.Fatalf("state = %q, want %q", report.State, tt.wantState)
			}
			if !reflect.DeepEqual(calls, tt.wantCalls) {
				t.Fatalf("calls = %v, want %v", calls, tt.wantCalls)
			}
		})
	}
}

func errorsIs(got, want error) bool {
	if want == nil {
		return got == nil
	}
	return got == want
}
