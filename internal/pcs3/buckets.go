package pcs3

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// CreateBucket создаёт бакет и ждёт, пока HeadBucket его увидит. Без ожидания
// зависимые ресурсы, которые Terraform запускает сразу после создания бакета,
// могут получить NoSuchBucket, пока новый бакет не стал виден.
func CreateBucket(ctx context.Context, client *s3.Client, bucketName string) error {
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	}); err != nil {
		return err
	}
	// Только перечитывание, без повтора записи: повторный create-bucket на уже
	// созданном бакете вернул бы BucketAlreadyOwnedByYou.
	return retryUntilSuccess(ctx, 10*time.Second, fmt.Sprintf("бакет %q", bucketName), func() error {
		return HeadBucket(ctx, client, bucketName)
	})
}

// Удаление бакета
func DeleteBucket(ctx context.Context, client *s3.Client, bucketName string) error {
	_, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucketName),
	})
	return err
}

// HeadBucket проверяет, существует ли бакет и есть ли к нему доступ
func HeadBucket(ctx context.Context, client *s3.Client, bucketName string) error {
	return retryTransientRead(ctx, func() error {
		_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
			Bucket: aws.String(bucketName),
		})
		return err
	})
}

// ListBuckets выводит список всех доступных бакетов
func ListBuckets(ctx context.Context, client *s3.Client) ([]string, error) {
	output, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, err
	}

	var buckets []string
	for _, b := range output.Buckets {
		if b.Name != nil {
			buckets = append(buckets, *b.Name)
		}
	}
	return buckets, nil
}

// GetBucketStats подсчитывает количество объектов и занимаемый объем внутри бакета
func GetBucketStats(ctx context.Context, client *s3.Client, bucketName string) (int64, int64, error) {
	var totalCount int64
	var totalSize int64
	var continuationToken *string

	for {
		output, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucketName),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return 0, 0, err
		}

		for _, obj := range output.Contents {
			totalCount++
			if obj.Size != nil {
				totalSize += *obj.Size
			}
		}

		if output.IsTruncated != nil && *output.IsTruncated {
			continuationToken = output.NextContinuationToken
		} else {
			break
		}
	}

	return totalCount, totalSize, nil
}

// Включение или отключение версионирования
func SetBucketVersioning(ctx context.Context, client *s3.Client, bucketName string, status types.BucketVersioningStatus) error {
	put := func() error {
		_, err := client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
			Bucket: aws.String(bucketName),
			VersioningConfiguration: &types.VersioningConfiguration{
				Status: status,
			},
		})
		return err
	}
	// Повтор записи до подтверждения чтением: после apply статус гарантированно
	// применён (см. retryWriteUntilConfirmed).
	return retryWriteUntilConfirmed(ctx, 10*time.Second, fmt.Sprintf("версионирование бакета %q", bucketName), put, func() error {
		got, err := GetBucketVersioning(ctx, client, bucketName)
		if err != nil {
			return err
		}
		if got != string(status) {
			return fmt.Errorf("GetBucketVersioning вернул %q, ожидали %q", got, status)
		}
		return nil
	})
}

// GetBucketVersioning возвращает текущий статус версионирования бакета
func GetBucketVersioning(ctx context.Context, client *s3.Client, bucketName string) (string, error) {
	output, err := client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return "", err
	}

	if output.Status != "" {
		return string(output.Status), nil
	}
	return "Off", nil
}

// Установка JSON-политики бакета
func SetBucketPolicy(ctx context.Context, client *s3.Client, bucketName string, policyJSON string) error {
	put := func() error {
		_, err := client.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
			Bucket: aws.String(bucketName),
			Policy: aws.String(policyJSON),
		})
		return err
	}
	return retryWriteUntilConfirmed(ctx, 10*time.Second, fmt.Sprintf("политика бакета %q", bucketName), put, func() error {
		got, err := GetBucketPolicy(ctx, client, bucketName)
		if err != nil {
			return err
		}
		if got == "" {
			return fmt.Errorf("get-bucket-policy пока возвращает пустую политику")
		}
		return nil
	})
}

// GetBucketPolicy возвращает JSON-политику бакета или "", если политики нет.
//
// «Политики нет» PlatformCraft сообщает одним из двух способов: ошибкой
// NoSuchBucketPolicy (функция возвращает её как есть) или, после
// delete-bucket-policy, пустым документом {"Version":"","Statement":null}.
// Пустой документ приводится к "".
func GetBucketPolicy(ctx context.Context, client *s3.Client, bucketName string) (string, error) {
	output, err := client.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return "", err
	}

	policy := aws.ToString(output.Policy)
	if IsEmptyPolicy(policy) {
		return "", nil
	}
	return policy, nil
}

// IsEmptyPolicy сообщает, что документ политики не содержит ни одного правила:
// пустая строка или Statement отсутствует, null либо пустой массив.
func IsEmptyPolicy(doc string) bool {
	if strings.TrimSpace(doc) == "" {
		return true
	}
	var p struct {
		Statement json.RawMessage `json:"Statement"`
	}
	if err := json.Unmarshal([]byte(doc), &p); err != nil {
		return false
	}
	switch strings.TrimSpace(string(p.Statement)) {
	case "", "null", "[]", "{}":
		return true
	}
	return false
}

// DeleteBucketPolicy удаляет политику бакета
func DeleteBucketPolicy(ctx context.Context, client *s3.Client, bucketName string) error {
	_, err := client.DeleteBucketPolicy(ctx, &s3.DeleteBucketPolicyInput{
		Bucket: aws.String(bucketName),
	})
	return err
}

// DeleteBucketPolicyConfirmed удаляет политику и проверяет чтением, что её
// больше нет (NoSuchBucketPolicy или пустой документ), повторяя удаление до
// maxWait. Возвращает removed=false без ошибки, если политика с правилами
// продолжает читаться. Ошибка — только если сам запрос удаления или чтения
// завершился ошибкой, отличной от «политики нет».
func DeleteBucketPolicyConfirmed(ctx context.Context, client *s3.Client, bucketName string, maxWait time.Duration) (removed bool, err error) {
	deadline := time.Now().Add(maxWait)
	delay := 200 * time.Millisecond
	for {
		if err := DeleteBucketPolicy(ctx, client, bucketName); err != nil && !isErrorCode(err, "NoSuchBucketPolicy") {
			return false, err
		}
		policy, err := GetBucketPolicy(ctx, client, bucketName)
		if isErrorCode(err, "NoSuchBucketPolicy") {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if policy == "" {
			return true, nil
		}
		if time.Now().Add(delay).After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(delay):
		}
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}

// PutObjectLockConfiguration включает Object Lock и задаёт retention по умолчанию.
// В отличие от Amazon S3, PlatformCraft не требует создавать бакет с флагом
// Object Lock: достаточно, чтобы версионирование было включено до этого вызова
// (в Terraform — depends_on на platformcraft_bucket_versioning).
func PutObjectLockConfiguration(ctx context.Context, client *s3.Client, bucketName string, mode types.ObjectLockRetentionMode, days int32) error {
	put := func() error {
		_, err := client.PutObjectLockConfiguration(ctx, &s3.PutObjectLockConfigurationInput{
			Bucket: aws.String(bucketName),
			ObjectLockConfiguration: &types.ObjectLockConfiguration{
				ObjectLockEnabled: types.ObjectLockEnabledEnabled,
				Rule: &types.ObjectLockRule{
					DefaultRetention: &types.DefaultRetention{
						Mode: mode, // types.ObjectLockRetentionModeCompliance или types.ObjectLockRetentionModeGovernance
						Days: aws.Int32(days),
					},
				},
			},
		})
		return err
	}
	return retryWriteUntilConfirmed(ctx, 10*time.Second, fmt.Sprintf("Object Lock конфигурация бакета %q", bucketName), put, func() error {
		cfg, err := GetObjectLockConfiguration(ctx, client, bucketName)
		if err != nil {
			return err
		}
		if cfg == nil || cfg.Rule == nil || cfg.Rule.DefaultRetention == nil {
			return fmt.Errorf("GetObjectLockConfiguration вернул пустое правило retention")
		}
		return nil
	})
}

// GetObjectLockConfiguration возвращает текущую конфигурацию Object Lock бакета.
// Если конфигурация не задана, PlatformCraft (как и AWS) обычно возвращает ошибку
// ObjectLockConfigurationNotFoundError — это нужно обрабатывать отдельно от прочих ошибок,
// т.к. для Terraform-провайдера это означает "ресурс не существует", а не сбой запроса.
func GetObjectLockConfiguration(ctx context.Context, client *s3.Client, bucketName string) (*types.ObjectLockConfiguration, error) {
	output, err := client.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return nil, err
	}
	return output.ObjectLockConfiguration, nil
}

// SetBucketAcl устанавливает canned ACL для бакета.
// Допустимые значения по доке PlatformCraft: private, public-read, public-read-write, authenticated-read.
func SetBucketAcl(ctx context.Context, client *s3.Client, bucketName string, acl types.BucketCannedACL) error {
	put := func() error {
		_, err := client.PutBucketAcl(ctx, &s3.PutBucketAclInput{
			Bucket: aws.String(bucketName),
			ACL:    acl,
		})
		return err
	}
	return retryWriteUntilConfirmed(ctx, 10*time.Second, fmt.Sprintf("ACL бакета %q", bucketName), put, func() error {
		_, _, err := GetBucketAcl(ctx, client, bucketName)
		return err
	})
}

// GetBucketAcl возвращает владельца и список грантов ACL бакета.
// Полезно и для проверки существования/доступности бакета (как альтернатива HeadBucket) —
// так и указано в доке PlatformCraft.
func GetBucketAcl(ctx context.Context, client *s3.Client, bucketName string) (*types.Owner, []types.Grant, error) {
	output, err := client.GetBucketAcl(ctx, &s3.GetBucketAclInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return nil, nil, err
	}
	return output.Owner, output.Grants, nil
}

// CorsRule — одно CORS-правило в терминах пакета pcs3 (без зависимости от
// s3/types в вызывающем коде провайдера).
type CorsRule struct {
	AllowedOrigins []string
	AllowedMethods []string
	AllowedHeaders []string
	MaxAgeSeconds  int32
}

// SetBucketCors устанавливает CORS-конфигурацию бакета из списка правил.
// Порядок правил, как в S3, значим: правила проверяются по порядку,
// применяется первое подошедшее.
func SetBucketCors(ctx context.Context, client *s3.Client, bucketName string, rules []CorsRule) error {
	corsRules := make([]types.CORSRule, 0, len(rules))
	for _, r := range rules {
		corsRules = append(corsRules, types.CORSRule{
			AllowedMethods: r.AllowedMethods,
			AllowedOrigins: r.AllowedOrigins,
			AllowedHeaders: r.AllowedHeaders,
			MaxAgeSeconds:  aws.Int32(r.MaxAgeSeconds),
		})
	}
	put := func() error {
		_, err := client.PutBucketCors(ctx, &s3.PutBucketCorsInput{
			Bucket: aws.String(bucketName),
			CORSConfiguration: &types.CORSConfiguration{
				CORSRules: corsRules,
			},
		})
		return err
	}
	// Сразу после put-bucket-cors чтение может ещё не видеть конфигурацию
	// (NoSuchBucketCors), поэтому запись повторяется до подтверждения. Сверяется количество правил: этого достаточно, чтобы
	// отличить «ещё не применилось» от применённой конфигурации.
	return retryWriteUntilConfirmed(ctx, 10*time.Second, fmt.Sprintf("CORS-конфигурация бакета %q", bucketName), put, func() error {
		got, err := GetBucketCors(ctx, client, bucketName)
		if err != nil {
			return err
		}
		if len(got) != len(rules) {
			return fmt.Errorf("GetBucketCors вернул %d правил, ожидали %d", len(got), len(rules))
		}
		return nil
	})
}

// GetBucketCors получает текущую CORS-конфигурацию бакета
func GetBucketCors(ctx context.Context, client *s3.Client, bucketName string) ([]types.CORSRule, error) {
	output, err := client.GetBucketCors(ctx, &s3.GetBucketCorsInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return nil, err
	}
	return output.CORSRules, nil
}

// DeleteBucketCors удаляет CORS-конфигурацию бакета
func DeleteBucketCors(ctx context.Context, client *s3.Client, bucketName string) error {
	_, err := client.DeleteBucketCors(ctx, &s3.DeleteBucketCorsInput{
		Bucket: aws.String(bucketName),
	})
	return err
}

// EmptyBucket удаляет из бакета все объекты, все их версии и delete-маркеры —
// то, что нужно сделать перед DeleteBucket, если бакет не пустой (S3 отвечает
// на удаление непустого бакета BucketNotEmpty, в том числе когда остались только
// старые версии объектов).
//
// Каждая версия удаляется отдельным DeleteObject с BypassGovernanceRetention:
// у пакетного DeleteObjects в PlatformCraft нет обхода Governance-блокировки.
// Если удаление версии не прошло, снимаем с неё legal hold и пробуем ещё раз.
// Версии под retention в режиме COMPLIANCE удалить нельзя никому — для них
// функция вернёт ошибку с перечнем ключей.
func EmptyBucket(ctx context.Context, client *s3.Client, bucketName string) error {
	deleteVersion := func(key, versionID *string) error {
		input := &s3.DeleteObjectInput{
			Bucket:                    aws.String(bucketName),
			Key:                       key,
			VersionId:                 versionID,
			BypassGovernanceRetention: aws.Bool(true),
		}
		if _, err := client.DeleteObject(ctx, input); err == nil {
			return nil
		}
		// Возможно, мешает legal hold — снимаем и повторяем. Ошибку снятия не
		// проверяем: если legal hold не было, итог покажет повторное удаление.
		_, _ = client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
			Bucket:    aws.String(bucketName),
			Key:       key,
			VersionId: versionID,
			LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOff},
		})
		_, err := client.DeleteObject(ctx, input)
		return err
	}

	var failed []string
	var lastErr error

	versions, markers, err := ListObjectVersions(ctx, client, bucketName)
	if err != nil {
		return fmt.Errorf("list-object-versions: %w", err)
	}
	for _, v := range versions {
		if err := deleteVersion(v.Key, v.VersionId); err != nil {
			failed = append(failed, aws.ToString(v.Key)+"@"+aws.ToString(v.VersionId))
			lastErr = err
		}
	}
	for _, m := range markers {
		if err := deleteVersion(m.Key, m.VersionId); err != nil {
			failed = append(failed, aws.ToString(m.Key)+"@"+aws.ToString(m.VersionId))
			lastErr = err
		}
	}

	// Объекты, которых нет в выдаче list-object-versions (бакет без
	// версионирования у бэкенда, который версии в этом случае не отдаёт).
	objects, err := ListObjectsV2(ctx, client, bucketName)
	if err != nil {
		return fmt.Errorf("list-objects-v2: %w", err)
	}
	for _, o := range objects {
		if err := deleteVersion(o.Key, nil); err != nil {
			failed = append(failed, aws.ToString(o.Key))
			lastErr = err
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("не удалось удалить %d версий объектов (например, %s): %w", len(failed), failed[0], lastErr)
	}
	return nil
}
