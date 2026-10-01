package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"github.com/example/course-avatar-pipeline/internal/avatar"
)

func main() {
	key := os.Getenv("INFRAI_API_KEY")
	if key == "" {
		log.Fatal("INFRAI_API_KEY is required")
	}
	baseURL := os.Getenv("INFRAI_BASE_URL")
	if baseURL == "" {
		baseURL = avatar.DefaultBaseURL
	}
	client := avatar.NewClient(baseURL, key)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /courses/{course_id}/learners/{learner_id}/avatar", uploadHandler(client))
	log.Printf("avatar service listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func uploadHandler(client *avatar.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart request"})
			return
		}
		deadline, err := time.Parse(time.RFC3339, r.FormValue("avatar_deadline"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "avatar_deadline must be RFC3339"})
			return
		}
		file, header, err := r.FormFile("avatar")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "avatar is required"})
			return
		}
		defer file.Close()
		image, err := readImage(file, header)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		report, err := client.Process(r.Context(), avatar.ProcessInput{
			Delivery: avatar.CourseDelivery{CourseID: r.PathValue("course_id"), LearnerID: r.PathValue("learner_id"), AvatarDeadline: deadline},
			Filename: header.Filename, Image: image, Now: time.Now(),
		})
		if err != nil {
			status := http.StatusBadGateway
			var apiErr *avatar.InfraiError
			switch {
			case errors.Is(err, avatar.ErrDeadlinePassed):
				status = http.StatusConflict
			case errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status < 500:
				status = apiErr.Status
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

func readImage(file multipart.File, header *multipart.FileHeader) ([]byte, error) {
	if header.Size > 8<<20 {
		return nil, errors.New("avatar exceeds 8 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, (8<<20)+1))
	if err != nil {
		return nil, errors.New("cannot read avatar")
	}
	if len(data) > 8<<20 {
		return nil, fmt.Errorf("avatar exceeds 8 MiB")
	}
	return data, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
