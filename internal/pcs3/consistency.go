package pcs3

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/smithy-go"
)

// retryUntilSuccess опрашивает check() с растущей паузой (200ms -> ... -> максимум 2s
// между попытками), пока он не вернёт nil, либо не истечёт maxWait.
//
// Только перечитывает, саму запись не повторяет. Подходит для операций, повтор
// которых бессмыслен или небезопасен (create-bucket): запись прошла, ждём, пока
// результат станет виден при чтении. Для идемпотентных put/set-операций —
// retryWriteUntilConfirmed.
func retryUntilSuccess(ctx context.Context, maxWait time.Duration, resourceDescription string, check func() error) error {
	delay := 200 * time.Millisecond
	deadline := time.Now().Add(maxWait)

	var lastErr error
	for {
		if err := check(); err == nil {
			return nil
		} else {
			lastErr = err
		}

		if time.Now().Add(delay).After(deadline) {
			return fmt.Errorf(
				"%s — операция записи отработала успешно, но чтение всё ещё не подтверждает изменение спустя %s. "+
					"Вероятно, изменение ещё не распространилось; попробуйте применить ресурс ещё раз. "+
					"Последняя ошибка чтения: %w",
				resourceDescription, maxWait, lastErr,
			)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}

// retryWriteUntilConfirmed делает write(), затем проверяет check() с растущей
// паузой; если check() не подтвердился — повторяет write() перед следующей
// проверкой, а не только перечитывает.
//
// Так провайдер гарантирует, что после успешного apply настройка действительно
// применена и видна при чтении, даже если отдельный запрос записи не дошёл до
// хранилища или его результат проявился с задержкой.
//
// write должен быть идемпотентным (повторный вызов с теми же параметрами не
// должен быть опасен) — все Put/Set-операции в этом пакете такие по своей
// природе (полностью перезаписывают состояние, не инкрементируют).
func retryWriteUntilConfirmed(ctx context.Context, maxWait time.Duration, resourceDescription string, write func() error, check func() error) error {
	if err := write(); err != nil {
		return err
	}

	delay := 200 * time.Millisecond
	deadline := time.Now().Add(maxWait)

	var lastErr error
	for {
		if err := check(); err == nil {
			return nil
		} else {
			lastErr = err
		}

		if time.Now().Add(delay).After(deadline) {
			return fmt.Errorf(
				"%s — запись выполнена, но чтение не подтвердило изменение за %s (запись повторялась). "+
					"Попробуйте применить ресурс ещё раз и проверьте состояние в панели PlatformCraft. "+
					"Последняя ошибка чтения: %w",
				resourceDescription, maxWait, lastErr,
			)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 2*time.Second {
			delay *= 2
		}

		if err := write(); err != nil {
			return err
		}
	}
}

// isLikelyTransientReadError отличает «бакета/объекта нет» (повторять
// бессмысленно — Read() должен сразу убрать ресурс из state) от остальных
// ошибок, которые при одиночном чтении имеет смысл повторить, например
// разового отказа HeadObject при кратковременной недоступности сервиса.
func isLikelyTransientReadError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchBucket", "NotFound", "404", "NoSuchKey":
			// Легитимный "не найдено" — Read() должен увидеть это сразу и
			// убрать ресурс из state, а не тратить секунды на повторы.
			return false
		}
	}
	return true
}

// retryTransientRead — короткий ограниченный повтор одиночного чтения
// (HeadObject/HeadBucket): 2 попытки с паузой в 1 секунду. В отличие от
// retryUntilSuccess/retryWriteUntilConfirmed здесь не ждём конкретного значения
// после записи, а сглаживаем разовый сбой API. Настоящий отказ в доступе или
// удалённый объект проявятся не более чем на секунду позже.
func retryTransientRead(ctx context.Context, read func() error) error {
	const attempts = 2
	const delay = 1 * time.Second

	var lastErr error
	for i := 0; i < attempts; i++ {
		err := read()
		if err == nil {
			return nil
		}
		lastErr = err
		if !isLikelyTransientReadError(err) {
			return err
		}
		if i < attempts-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return lastErr
}

// isErrorCode сообщает, что err — ошибка API с заданным кодом.
func isErrorCode(err error, code string) bool {
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode() == code
}
