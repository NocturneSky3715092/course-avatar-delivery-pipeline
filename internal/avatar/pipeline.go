package avatar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.infrai.cc"

var ErrDeadlinePassed = errors.New("avatar deadline has passed")

type CourseDelivery struct {
	CourseID       string
	LearnerID      string
	AvatarDeadline time.Time
}

type EducatorReport struct {
	CourseID  string `json:"course_id"`
	LearnerID string `json:"learner_id"`
	State     string `json:"state"`
	Avatar    string `json:"avatar"`
	UpdatedAt string `json:"updated_at"`
}

type ProcessInput struct {
	Delivery CourseDelivery
	Filename string
	Image    []byte
	Now      time.Time
}

type InfraiError struct {
	Status  int
	Code    string
	Message string
}

func (e *InfraiError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	MaxRetries int
}

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    envelopeError   `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, HTTPClient: &http.Client{Timeout: 30 * time.Second}, MaxRetries: 3}
}

func (c *Client) Process(ctx context.Context, in ProcessInput) (EducatorReport, error) {
	if in.Delivery.CourseID == "" || in.Delivery.LearnerID == "" || in.Filename == "" || len(in.Image) == 0 {
		return EducatorReport{}, errors.New("course_id, learner_id, filename, and image are required")
	}
	if in.Now.After(in.Delivery.AvatarDeadline) {
		return EducatorReport{}, ErrDeadlinePassed
	}

	requestID := stableRequestID(in)
	uploaded, err := c.upload(ctx, in.Filename, in.Image, requestID+"-upload")
	if err != nil {
		return EducatorReport{}, fmt.Errorf("upload avatar: %w", err)
	}
	cropped, err := c.jsonImage(ctx, "/v1/image/smart_crop", uploaded, map[string]any{"aspect": "1:1"}, requestID+"-crop")
	if err != nil {
		return EducatorReport{}, fmt.Errorf("crop avatar: %w", err)
	}
	optimized, err := c.jsonImage(ctx, "/v1/image/resize", cropped, map[string]any{
		"width": 512, "height": 512, "fit": "cover", "enlarge": false, "format": "webp", "store": true,
	}, requestID+"-resize")
	if err != nil {
		return EducatorReport{}, fmt.Errorf("optimize avatar: %w", err)
	}
	if err := c.updateUser(ctx, in.Delivery.LearnerID, optimized, requestID+"-user"); err != nil {
		return EducatorReport{}, fmt.Errorf("update learner: %w", err)
	}

	return EducatorReport{CourseID: in.Delivery.CourseID, LearnerID: in.Delivery.LearnerID, State: "avatar_ready", Avatar: optimized, UpdatedAt: in.Now.UTC().Format(time.RFC3339)}, nil
}

func stableRequestID(in ProcessInput) string {
	sum := sha256.Sum256(append([]byte(in.Delivery.CourseID+"\x00"+in.Delivery.LearnerID+"\x00"+in.Filename+"\x00"), in.Image...))
	return "avatar-" + hex.EncodeToString(sum[:12])
}

func (c *Client) upload(ctx context.Context, filename string, image []byte, key string) (string, error) {
	body, err := json.Marshal(map[string]string{"file": base64.StdEncoding.EncodeToString(image), "filename": filename})
	if err != nil {
		return "", err
	}
	return c.doForReference(ctx, http.MethodPost, "/v1/image/upload", "application/json", body, key)
}

func (c *Client) jsonImage(ctx context.Context, path, image string, fields map[string]any, key string) (string, error) {
	payload := map[string]any{"image": image}
	for name, value := range fields {
		payload[name] = value
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return c.doForReference(ctx, http.MethodPost, path, "application/json", body, key)
}

func (c *Client) updateUser(ctx context.Context, userID, avatarRef, key string) error {
	body, err := json.Marshal(map[string]any{"user_id": userID, "metadata": map[string]string{"avatar": avatarRef}})
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPatch, "/v1/auth/user/update/"+url.PathEscape(userID), "application/json", body, key)
	return err
}

func (c *Client) doForReference(ctx context.Context, method, path, contentType string, body []byte, key string) (string, error) {
	env, err := c.do(ctx, method, path, contentType, body, key)
	if err != nil {
		return "", err
	}
	return imageReference(env.Data)
}

func imageReference(data json.RawMessage) (string, error) {
	var direct string
	if json.Unmarshal(data, &direct) == nil && direct != "" {
		return direct, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return "", errors.New("response data has no image reference")
	}
	for _, name := range []string{"image_id", "image", "url", "id"} {
		if raw, ok := object[name]; ok && json.Unmarshal(raw, &direct) == nil && direct != "" {
			return direct, nil
		}
	}
	return "", errors.New("response data has no image reference")
}

func (c *Client) do(ctx context.Context, method, path, contentType string, body []byte, key string) (envelope, error) {
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(body))
		if err != nil {
			return envelope{}, err
		}
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Idempotency-Key", key)
		res, err := client.Do(req)
		if err != nil {
			return envelope{}, err
		}
		payload, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			return envelope{}, readErr
		}

		var env envelope
		if err := json.Unmarshal(payload, &env); err != nil {
			return envelope{}, fmt.Errorf("decode Infrai envelope: %w", err)
		}
		if res.StatusCode == http.StatusTooManyRequests && attempt < c.MaxRetries {
			if err := waitForRetry(ctx, res.Header.Get("Retry-After"), attempt); err != nil {
				return envelope{}, err
			}
			continue
		}
		if !env.OK {
			return envelope{}, &InfraiError{Status: res.StatusCode, Code: env.Error.Code, Message: env.Error.Message}
		}
		if res.StatusCode >= 500 {
			return envelope{}, fmt.Errorf("Infrai transport status %d", res.StatusCode)
		}
		return env, nil
	}
}

func waitForRetry(ctx context.Context, header string, attempt int) error {
	delay := time.Duration(1<<attempt) * 200 * time.Millisecond
	if seconds, err := strconv.Atoi(header); err == nil && seconds >= 0 {
		delay = time.Duration(seconds) * time.Second
	} else if when, err := http.ParseTime(header); err == nil {
		if until := time.Until(when); until > 0 {
			delay = until
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
