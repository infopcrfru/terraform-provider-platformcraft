package pcs3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/middleware"
)

func TestRequestLimiter_SpacesRequests(t *testing.T) {
	l := &requestLimiter{}
	l.setRate(100) // интервал 10 мс

	start := time.Now()
	for i := 0; i < 21; i++ {
		if err := l.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// 21 запрос при 100 rps: первый сразу, остальные 20 — через 10 мс каждый.
	if elapsed := time.Since(start); elapsed < 190*time.Millisecond {
		t.Errorf("21 запрос при 100 rps прошли за %s, ожидалось не меньше ~200 мс", elapsed)
	}
}

func TestRequestLimiter_ZeroDisables(t *testing.T) {
	l := &requestLimiter{}
	l.setRate(0)

	start := time.Now()
	for i := 0; i < 1000; i++ {
		if err := l.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("без ограничения 1000 вызовов заняли %s", elapsed)
	}
}

func TestRequestLimiter_RespectsContext(t *testing.T) {
	l := &requestLimiter{}
	l.setRate(1) // интервал 1 с
	_ = l.wait(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := l.wait(ctx); err == nil {
		t.Error("ожидалась ошибка отмены контекста")
	}
}

// TestRetryer_Retries429: ответ 429 с кодом ошибки, которого нет в списке
// троттлинга AWS SDK, повторяется, и запрос в итоге проходит.
func TestRetryer_Retries429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>TooManyRequests</Code><Message>rate limit</Message></Error>`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := s3.New(s3.Options{
		Region:       "eu-central-2",
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider("ak", "sk", ""),
		APIOptions:   []func(*middleware.Stack) error{RateLimitAPIOption(0)},
		Retryer:      retry.AddWithMaxBackoffDelay(NewRetryer(), 10*time.Millisecond),
	})

	if _, err := client.HeadBucket(context.Background(), &s3.HeadBucketInput{Bucket: aws.String("b")}); err != nil {
		t.Fatalf("HeadBucket после двух 429: %s", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("ожидалось 3 запроса (два 429 и успешный), было %d", got)
	}
}

// TestRateLimitAPIOption_LimitsEachAttempt: ограничение действует на каждую
// попытку, включая повторы после 429.
func TestRateLimitAPIOption_LimitsEachAttempt(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1)%2 == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := s3.New(s3.Options{
		Region:       "eu-central-2",
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider("ak", "sk", ""),
		APIOptions:   []func(*middleware.Stack) error{RateLimitAPIOption(50)}, // 20 мс между попытками
		Retryer:      retry.AddWithMaxBackoffDelay(NewRetryer(), time.Millisecond),
	})
	defer processLimiter.setRate(0)

	start := time.Now()
	for i := 0; i < 5; i++ {
		if _, err := client.HeadBucket(context.Background(), &s3.HeadBucketInput{Bucket: aws.String("b")}); err != nil {
			t.Fatal(err)
		}
	}
	// 5 операций по 2 попытки = 10 запросов при 50 rps: не меньше ~180 мс.
	if got := atomic.LoadInt32(&calls); got != 10 {
		t.Fatalf("ожидалось 10 запросов, было %d", got)
	}
	if elapsed := time.Since(start); elapsed < 170*time.Millisecond {
		t.Errorf("10 попыток при 50 rps заняли %s, ожидалось не меньше ~180 мс", elapsed)
	}
}
