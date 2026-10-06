package pcs3

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/ratelimit"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/smithy-go/middleware"
)

// Лимиты PlatformCraft по умолчанию: 70 запросов в секунду и 240 Мбит/с на
// одного клиента (оба значения расширяются по запросу). Превышение частоты
// запросов API отвечает HTTP 429.
//
// Провайдер держит частоту запросов ниже лимита сам (RateLimitAPIOption),
// а если 429 всё же пришёл (например, параллельно работает aws-cli под теми же
// ключами), повторяет запрос с экспоненциальной паузой (NewRetryer).

// DefaultMaxRequestsPerSecond — частота запросов провайдера по умолчанию.
// Меньше лимита PlatformCraft (70), чтобы оставался запас для других клиентов
// с теми же ключами.
const DefaultMaxRequestsPerSecond = 50

// requestLimiter равномерно распределяет запросы во времени: не больше rps в
// секунду, без всплесков. Один на процесс (processLimiter): все клиенты,
// созданные в процессе провайдера или тестов, делят общий лимит.
type requestLimiter struct {
	mu       sync.Mutex
	interval time.Duration // 0 — без ограничения
	next     time.Time
}

var processLimiter = &requestLimiter{}

func (l *requestLimiter) setRate(rps int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rps <= 0 {
		l.interval = 0
		return
	}
	l.interval = time.Second / time.Duration(rps)
}

// wait блокирует до момента, когда можно отправить следующий запрос.
func (l *requestLimiter) wait(ctx context.Context) error {
	l.mu.Lock()
	if l.interval == 0 {
		l.mu.Unlock()
		return nil
	}
	now := time.Now()
	if l.next.Before(now) {
		l.next = now
	}
	delay := l.next.Sub(now)
	l.next = l.next.Add(l.interval)
	l.mu.Unlock()

	if delay <= 0 {
		return nil
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

// RateLimitAPIOption задаёт общую для процесса частоту запросов (rps <= 0 —
// без ограничения) и возвращает опцию стека AWS SDK, которая перед каждой
// попыткой отправки запроса, включая повторы, ждёт своей очереди.
//
// Ограничение сделано middleware, а не обёрткой HTTP-клиента: SDK требует свой
// *BuildableClient, чтобы подключить AWS_CA_BUNDLE (корпоративные прокси).
func RateLimitAPIOption(rps int) func(*middleware.Stack) error {
	processLimiter.setRate(rps)
	return func(stack *middleware.Stack) error {
		mw := middleware.FinalizeMiddlewareFunc("PlatformCraftRateLimit", func(
			ctx context.Context, in middleware.FinalizeInput, next middleware.FinalizeHandler,
		) (middleware.FinalizeOutput, middleware.Metadata, error) {
			if err := processLimiter.wait(ctx); err != nil {
				return middleware.FinalizeOutput{}, middleware.Metadata{}, err
			}
			return next.HandleFinalize(ctx, in)
		})
		// После Retry — значит, внутри цикла повторов: ограничивается каждая попытка.
		if err := stack.Finalize.Insert(mw, "Retry", middleware.After); err != nil {
			return stack.Finalize.Add(mw, middleware.After)
		}
		return nil
	}
}

// NewRetryer — стандартный механизм повторов AWS SDK с поправками под PlatformCraft:
//   - HTTP 429 (превышен лимит запросов) повторяется. В S3 для этого принят
//     503 SlowDown, который SDK повторяет сам, а 429 — только если код ошибки
//     входит в список троттлинга SDK;
//   - до 6 попыток с экспоненциальной паузой (до 20 с);
//   - без клиентской квоты на повторы: при серии 429 квота SDK быстро
//     заканчивается, и запросы начинают падать без повтора.
func NewRetryer() aws.Retryer {
	return retry.NewStandard(func(o *retry.StandardOptions) {
		o.MaxAttempts = 6
		o.RateLimiter = ratelimit.None
		o.Retryables = append(o.Retryables, retry.RetryableHTTPStatusCode{
			Codes: map[int]struct{}{http.StatusTooManyRequests: {}},
		})
	})
}
