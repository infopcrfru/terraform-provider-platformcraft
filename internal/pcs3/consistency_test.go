package pcs3

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/smithy-go"
)

// Модульные тесты для consistency.go — с поддельными write/check, без сети.
// maxWait в тестах короткий (доли секунды), чтобы не растягивать `go test`;
// сама логика (растущая пауза, повторный write, таймаут) от этого не меняется.

func TestRetryUntilSuccess_SucceedsFirstTry(t *testing.T) {
	calls := 0
	err := retryUntilSuccess(context.Background(), time.Second, "тест", func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("ожидали nil, получили %v", err)
	}
	if calls != 1 {
		t.Errorf("ожидали ровно 1 вызов check(), получили %d", calls)
	}
}

func TestRetryUntilSuccess_SucceedsAfterRetries(t *testing.T) {
	calls := 0
	err := retryUntilSuccess(context.Background(), 2*time.Second, "тест", func() error {
		calls++
		if calls < 3 {
			return errors.New("ещё не готово")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ожидали nil после нескольких попыток, получили %v", err)
	}
	if calls != 3 {
		t.Errorf("ожидали ровно 3 вызова check(), получили %d", calls)
	}
}

func TestRetryUntilSuccess_TimesOut(t *testing.T) {
	calls := 0
	err := retryUntilSuccess(context.Background(), 500*time.Millisecond, "тест-бакет", func() error {
		calls++
		return errors.New("никогда не подтверждается")
	})
	if err == nil {
		t.Fatal("ожидали ошибку по истечении maxWait, получили nil")
	}
	if calls < 2 {
		t.Errorf("ожидали хотя бы 2 попытки check() за 500ms при старте с 200ms паузы, получили %d", calls)
	}
}

func TestRetryUntilSuccess_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // отменяем сразу

	err := retryUntilSuccess(ctx, time.Second, "тест", func() error {
		return errors.New("не важно, context уже отменён")
	})
	if err == nil {
		t.Fatal("ожидали ошибку при отменённом context, получили nil")
	}
}

func TestRetryWriteUntilConfirmed_SucceedsFirstTry(t *testing.T) {
	writeCalls, checkCalls := 0, 0
	err := retryWriteUntilConfirmed(context.Background(), time.Second, "тест",
		func() error { writeCalls++; return nil },
		func() error { checkCalls++; return nil },
	)
	if err != nil {
		t.Fatalf("ожидали nil, получили %v", err)
	}
	if writeCalls != 1 {
		t.Errorf("ожидали ровно 1 вызов write(), получили %d", writeCalls)
	}
	if checkCalls != 1 {
		t.Errorf("ожидали ровно 1 вызов check(), получили %d", checkCalls)
	}
}

func TestRetryWriteUntilConfirmed_WriteErrorReturnsImmediately(t *testing.T) {
	checkCalls := 0
	wantErr := errors.New("PutBucketVersioning: 403 AccessDenied")

	err := retryWriteUntilConfirmed(context.Background(), time.Second, "тест",
		func() error { return wantErr },
		func() error { checkCalls++; return nil },
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("ожидали, что ошибка write() вернётся как есть, получили %v", err)
	}
	if checkCalls != 0 {
		t.Errorf("check() не должен вызываться, если сам write() упал; вызван %d раз", checkCalls)
	}
}

// TestRetryWriteUntilConfirmed_RetriesWriteWhenCheckNeverConfirms: если
// write() отвечает успехом, но check() не видит изменение, retry должен
// повторно вызывать write(), а не только check().
func TestRetryWriteUntilConfirmed_RetriesWriteWhenCheckNeverConfirms(t *testing.T) {
	writeCalls, checkCalls := 0, 0

	err := retryWriteUntilConfirmed(context.Background(), 700*time.Millisecond, "тест-объект",
		func() error { writeCalls++; return nil },
		func() error { checkCalls++; return errors.New("check никогда не подтверждает") },
	)
	if err == nil {
		t.Fatal("ожидали ошибку по истечении maxWait, получили nil")
	}
	if writeCalls < 2 {
		t.Errorf("ожидали, что write() будет вызван повторно (не только один раз), вызван %d раз", writeCalls)
	}
	if checkCalls < 2 {
		t.Errorf("ожидали несколько вызовов check(), получили %d", checkCalls)
	}
}

func TestRetryWriteUntilConfirmed_SucceedsAfterOneRetry(t *testing.T) {
	writeCalls, checkCalls := 0, 0

	err := retryWriteUntilConfirmed(context.Background(), 2*time.Second, "тест",
		func() error { writeCalls++; return nil },
		func() error {
			checkCalls++
			if checkCalls < 2 {
				return errors.New("ещё не подтверждено")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("ожидали nil, получили %v", err)
	}
	if writeCalls < 2 {
		t.Errorf("ожидали как минимум 2 вызова write() (исходный + повтор), получили %d", writeCalls)
	}
}

// TestRetryTransientRead_SucceedsAfterOneTransientError: разовый отказ при
// чтении существующего объекта, повтор через секунду проходит.
func TestRetryTransientRead_SucceedsAfterOneTransientError(t *testing.T) {
	calls := 0
	err := retryTransientRead(context.Background(), func() error {
		calls++
		if calls == 1 {
			return &smithy.GenericAPIError{Code: "Forbidden", Message: "Forbidden"}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ожидали nil после одного транзиентного сбоя, получили %v", err)
	}
	if calls != 2 {
		t.Errorf("ожидали ровно 2 попытки, получили %d", calls)
	}
}

func TestRetryTransientRead_DoesNotRetryNotFound(t *testing.T) {
	calls := 0
	err := retryTransientRead(context.Background(), func() error {
		calls++
		return &smithy.GenericAPIError{Code: "NoSuchBucket", Message: "не существует"}
	})
	if err == nil {
		t.Fatal("ожидали ошибку, получили nil")
	}
	if calls != 1 {
		t.Errorf("NotFound-класс ошибок не должен ретраиться — ожидали 1 попытку, получили %d", calls)
	}
}

func TestRetryTransientRead_GivesUpAfterMaxAttempts(t *testing.T) {
	calls := 0
	err := retryTransientRead(context.Background(), func() error {
		calls++
		return &smithy.GenericAPIError{Code: "Forbidden", Message: "Forbidden"}
	})
	if err == nil {
		t.Fatal("ожидали ошибку по истечении попыток, получили nil")
	}
	if calls != 2 {
		t.Errorf("ожидали ровно 2 попытки (константа attempts=2 в retryTransientRead), получили %d", calls)
	}
}

func TestIsEmptyPolicy(t *testing.T) {
	cases := map[string]bool{
		``:                                true,
		`  `:                              true,
		`{"Version":"","Statement":null}`: true, // ответ PlatformCraft после удаления политики
		`{"Version":"2012-10-17","Statement":[]}`: true,
		`{"Version":"2012-10-17"}`:                true,
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}]}`: false,
		`{"Version":"2012-10-17","Statement":{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}}`:   false,
		`не JSON`: false,
	}
	for doc, want := range cases {
		if got := IsEmptyPolicy(doc); got != want {
			t.Errorf("IsEmptyPolicy(%q) = %v, want %v", doc, got, want)
		}
	}
}
