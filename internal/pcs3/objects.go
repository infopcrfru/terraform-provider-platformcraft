package pcs3

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Все функции пакета принимают ctx первым параметром — это контекст, который
// Terraform Plugin Framework передаёт в Create/Read/Update/Delete, так что
// отмена операции (Ctrl+C, таймаут) доходит до HTTP-запросов к API.

// PutObject загружает объект в бакет. Использует manager.Uploader из aws-sdk-go-v2:
// он сам решает, грузить ли объект одним PUT или разбить на части (multipart),
// если body больше внутреннего порога — то есть отдельно реализовывать
// create-multipart-upload/upload-part/complete-multipart-upload не требуется,
// это уже инкапсулировано в SDK.
func PutObject(ctx context.Context, client *s3.Client, bucketName, key string, body io.Reader) (string, error) {
	uploader := manager.NewUploader(client)
	output, err := uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   body,
	})
	if err != nil {
		return "", err
	}
	return aws.ToString(output.ETag), nil
}

// GetObject скачивает объект и возвращает его тело. Вызывающий код обязан
// закрыть возвращённый io.ReadCloser.
func GetObject(ctx context.Context, client *s3.Client, bucketName, key string) (io.ReadCloser, error) {
	output, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

// HeadObject возвращает метаданные объекта без скачивания тела.
func HeadObject(ctx context.Context, client *s3.Client, bucketName, key string) (*s3.HeadObjectOutput, error) {
	var out *s3.HeadObjectOutput
	err := retryTransientRead(ctx, func() error {
		var innerErr error
		out, innerErr = client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(key),
		})
		return innerErr
	})
	return out, err
}

// copySourceHeader собирает значение заголовка x-amz-copy-source. По спецификации
// S3 он должен быть URL-encoded, а aws-sdk-go-v2 сам его НЕ кодирует: без этого
// ключи с пробелами, "+", "%", "?", "#" или кириллицей копируются с ошибкой
// (NoSuchKey / SignatureDoesNotMatch). "/" внутри ключа сохраняется как есть —
// он разделяет "папки" и кодироваться не должен.
func copySourceHeader(bucket, key string) string {
	segments := strings.Split(key, "/")
	for i, seg := range segments {
		// PathEscape оставляет "+" как есть, но часть S3-совместимых бэкендов
		// декодирует его в пробел (form-encoding) — кодируем явно, как aws-cli.
		segments[i] = strings.ReplaceAll(url.PathEscape(seg), "+", "%2B")
	}
	return url.PathEscape(bucket) + "/" + strings.Join(segments, "/")
}

// CopyObject копирует объект внутри Object Storage (в т.ч. между разными бакетами).
func CopyObject(ctx context.Context, client *s3.Client, srcBucket, srcKey, dstBucket, dstKey string) error {
	copySource := copySourceHeader(srcBucket, srcKey)
	_, err := client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(dstBucket),
		Key:        aws.String(dstKey),
		CopySource: aws.String(copySource),
	})
	return err
}

// DeleteObject удаляет один объект по ключу. bypassGovernanceRetention=true нужен,
// если объект защищён Object Lock в режиме Governance и его требуется удалить
// досрочно (для Compliance-режима это НЕ сработает — его нельзя обойти вообще
// никому, это заложено в саму семантику режима).
func DeleteObject(ctx context.Context, client *s3.Client, bucketName, key string, bypassGovernanceRetention bool) error {
	_, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:                    aws.String(bucketName),
		Key:                       aws.String(key),
		BypassGovernanceRetention: aws.Bool(bypassGovernanceRetention),
	})
	return err
}

// DeleteObjects удаляет пакет объектов по списку ключей за один запрос.
// По документации PlatformCraft — не более 1000 ключей за раз.
// В отличие от DeleteObject, у batch-удаления PlatformCraft НЕТ параметра обхода
// Governance-lock'а (в доке он указан только у одиночного delete-object) — если
// среди ключей есть залоченные объекты, для них используйте DeleteObject с
// bypassGovernanceRetention=true по одному, а не этот метод.
// Возвращает удалённые ключи и ошибки по отдельным ключам.
func DeleteObjects(ctx context.Context, client *s3.Client, bucketName string, keys []string) ([]string, []types.Error, error) {
	objects := make([]types.ObjectIdentifier, 0, len(keys))
	for _, k := range keys {
		objects = append(objects, types.ObjectIdentifier{Key: aws.String(k)})
	}

	output, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(bucketName),
		Delete: &types.Delete{Objects: objects},
	})
	if err != nil {
		return nil, nil, err
	}

	deleted := make([]string, 0, len(output.Deleted))
	for _, d := range output.Deleted {
		deleted = append(deleted, aws.ToString(d.Key))
	}
	return deleted, output.Errors, nil
}

// ListObjects — листинг объектов через v1 API (list-objects, не list-objects-v2)
// с полной пагинацией, как в ListObjectsV2, но через Marker/NextMarker
// (v1-протокол) вместо ContinuationToken (v2).
func ListObjects(ctx context.Context, client *s3.Client, bucketName string) ([]types.Object, error) {
	var all []types.Object
	var marker *string

	for {
		output, err := client.ListObjects(ctx, &s3.ListObjectsInput{
			Bucket: aws.String(bucketName),
			Marker: marker,
		})
		if err != nil {
			return nil, err
		}

		all = append(all, output.Contents...)

		if output.IsTruncated != nil && *output.IsTruncated {
			// NextMarker присутствует не всегда (только если запрос шёл с
			// Delimiter) — v1 API в этом случае предписывает брать Key
			// последнего элемента как следующий Marker.
			if output.NextMarker != nil {
				marker = output.NextMarker
			} else if len(output.Contents) > 0 {
				marker = output.Contents[len(output.Contents)-1].Key
			} else {
				break
			}
		} else {
			break
		}
	}

	return all, nil
}

// ListObjectsV2 листит все объекты бакета с автоматической пагинацией
// (PlatformCraft отдаёт не больше 10000 объектов за один запрос).
func ListObjectsV2(ctx context.Context, client *s3.Client, bucketName string) ([]types.Object, error) {
	var all []types.Object
	var continuationToken *string

	for {
		output, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucketName),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, err
		}

		all = append(all, output.Contents...)

		if output.IsTruncated != nil && *output.IsTruncated {
			continuationToken = output.NextContinuationToken
		} else {
			break
		}
	}

	return all, nil
}

// ListObjectVersions возвращает все версии объектов и delete-маркеры бакета
// (с пагинацией через KeyMarker/VersionIdMarker — за один запрос API отдаёт не
// больше 1000 записей). Имеет смысл только при включённом версионировании.
func ListObjectVersions(ctx context.Context, client *s3.Client, bucketName string) ([]types.ObjectVersion, []types.DeleteMarkerEntry, error) {
	var versions []types.ObjectVersion
	var markers []types.DeleteMarkerEntry
	var keyMarker, versionIDMarker *string

	for {
		output, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
			Bucket:          aws.String(bucketName),
			KeyMarker:       keyMarker,
			VersionIdMarker: versionIDMarker,
		})
		if err != nil {
			return nil, nil, err
		}
		versions = append(versions, output.Versions...)
		markers = append(markers, output.DeleteMarkers...)

		truncated := output.IsTruncated != nil && *output.IsTruncated
		if !truncated || (output.NextKeyMarker == nil && output.NextVersionIdMarker == nil) {
			break
		}
		// Защита от бесконечного цикла, если API вернёт IsTruncated=true с теми
		// же маркерами, что мы только что отправили.
		if aws.ToString(output.NextKeyMarker) == aws.ToString(keyMarker) &&
			aws.ToString(output.NextVersionIdMarker) == aws.ToString(versionIDMarker) {
			return nil, nil, fmt.Errorf("list-object-versions: API вернул IsTruncated=true, но не сдвинул маркеры (KeyMarker=%q, VersionIdMarker=%q)",
				aws.ToString(keyMarker), aws.ToString(versionIDMarker))
		}
		keyMarker = output.NextKeyMarker
		versionIDMarker = output.NextVersionIdMarker
	}
	return versions, markers, nil
}

// PutObjectLegalHold включает или снимает legal hold с объекта.
// Требует, чтобы у бакета был включён Object Lock (PutObjectLockConfiguration в buckets.go),
// который в свою очередь требует включённого версионирования.
func PutObjectLegalHold(ctx context.Context, client *s3.Client, bucketName, key string, on bool) error {
	status := types.ObjectLockLegalHoldStatusOff
	if on {
		status = types.ObjectLockLegalHoldStatusOn
	}
	put := func() error {
		_, err := client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
			Bucket:    aws.String(bucketName),
			Key:       aws.String(key),
			LegalHold: &types.ObjectLockLegalHold{Status: status},
		})
		return err
	}
	// Повтор записи до подтверждения чтением (см. retryWriteUntilConfirmed);
	// для legal hold ожидание больше — 30 с.
	return retryWriteUntilConfirmed(ctx, 30*time.Second, fmt.Sprintf("legal hold объекта %q/%q", bucketName, key), put, func() error {
		got, err := GetObjectLegalHold(ctx, client, bucketName, key)
		if err != nil {
			return err
		}
		if got != status {
			return fmt.Errorf("GetObjectLegalHold вернул %q, ожидали %q", got, status)
		}
		return nil
	})
}

// GetObjectLegalHold возвращает текущий статус legal hold объекта (ON/OFF).
// Используется в Read() ресурса, чтобы обнаружить дрифт, если legal hold
// сняли или включили вне Terraform (например, в панели PlatformCraft).
func GetObjectLegalHold(ctx context.Context, client *s3.Client, bucketName, key string) (types.ObjectLockLegalHoldStatus, error) {
	output, err := client.GetObjectLegalHold(ctx, &s3.GetObjectLegalHoldInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", err
	}
	if output.LegalHold == nil {
		return "", nil
	}
	return output.LegalHold.Status, nil
}

// PutObjectRetention устанавливает retention-период для объекта (конкретной версии,
// если задан versionID — иначе применяется к текущей версии).
// Для изменения уже существующего Governance-режима необходимо передавать bypassGovernance = true.
func PutObjectRetention(ctx context.Context, client *s3.Client, bucketName, key string, mode types.ObjectLockRetentionMode, retainUntil time.Time, bypassGovernance bool) error {
	put := func() error {
		_, err := client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(key),
			Retention: &types.ObjectLockRetention{
				Mode:            mode,
				RetainUntilDate: aws.Time(retainUntil),
			},
			BypassGovernanceRetention: aws.Bool(bypassGovernance),
		})
		return err
	}
	// Повтор записи до подтверждения чтением (см. retryWriteUntilConfirmed). Точное время не
	// сверяем (RetainUntilDate туда-обратно может немного разойтись по
	// округлению) — достаточно того, что GetObjectRetention вообще перестал
	// быть пустым и режим совпал.
	return retryWriteUntilConfirmed(ctx, 20*time.Second, fmt.Sprintf("retention объекта %q/%q", bucketName, key), put, func() error {
		got, err := GetObjectRetention(ctx, client, bucketName, key)
		if err != nil {
			return err
		}
		if got == nil {
			return fmt.Errorf("GetObjectRetention вернул пустой retention")
		}
		if got.Mode != mode {
			return fmt.Errorf("GetObjectRetention вернул mode=%q, ожидали %q", got.Mode, mode)
		}
		return nil
	})
}

// GetObjectRetention возвращает текущий retention объекта.
func GetObjectRetention(ctx context.Context, client *s3.Client, bucketName, key string) (*types.ObjectLockRetention, error) {
	output, err := client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	return output.Retention, nil
}

// newPresignClient — presign-клиент без служебного параметра x-id в URL.
//
// aws-sdk-go-v2 добавляет в URL каждой операции служебный параметр
// x-id=<Операция> (например, x-id=GetObject). PlatformCraft принимает presigned
// URL без него — в том виде, в каком их формирует `aws s3 presign`. Параметр
// чисто информационный, поэтому из presigned URL он убирается до подписи.
func newPresignClient(client *s3.Client) *s3.PresignClient {
	return s3.NewPresignClient(client, s3.WithPresignClientFromClientOptions(func(o *s3.Options) {
		opts := make([]func(*middleware.Stack) error, 0, len(o.APIOptions)+1)
		opts = append(opts, o.APIOptions...)
		o.APIOptions = append(opts, removeXIDQueryParam)
	}))
}

const removeXIDMiddlewareID = "PlatformCraftRemoveXID"

// removeXIDQueryParam удаляет x-id из строки запроса, не трогая порядок и
// кодирование остальных параметров.
func removeXIDQueryParam(stack *middleware.Stack) error {
	if _, ok := stack.Build.Get(removeXIDMiddlewareID); ok {
		return nil
	}
	return stack.Build.Add(middleware.BuildMiddlewareFunc(removeXIDMiddlewareID, func(
		ctx context.Context, in middleware.BuildInput, next middleware.BuildHandler,
	) (middleware.BuildOutput, middleware.Metadata, error) {
		if req, ok := in.Request.(*smithyhttp.Request); ok && req.URL.RawQuery != "" {
			parts := strings.Split(req.URL.RawQuery, "&")
			kept := parts[:0]
			for _, p := range parts {
				if p != "x-id" && !strings.HasPrefix(p, "x-id=") {
					kept = append(kept, p)
				}
			}
			req.URL.RawQuery = strings.Join(kept, "&")
		}
		return next.HandleBuild(ctx, in)
	}), middleware.After)
}

// PresignGetObject возвращает предподписанную ссылку на скачивание объекта,
// действующую в течение expires.
func PresignGetObject(ctx context.Context, client *s3.Client, bucketName, key string, expires time.Duration) (string, error) {
	presignClient := newPresignClient(client)
	req, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expires))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// PresignPutObject возвращает предподписанную ссылку на загрузку объекта,
// действующую в течение expires.
func PresignPutObject(ctx context.Context, client *s3.Client, bucketName, key string, expires time.Duration) (string, error) {
	presignClient := newPresignClient(client)
	req, err := presignClient.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expires))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// PutObjectAcl устанавливает canned ACL для объекта — то же самое, что SetBucketAcl
// в buckets.go, но на уровне конкретного объекта, а не всего бакета.
func PutObjectAcl(ctx context.Context, client *s3.Client, bucketName, key string, acl types.ObjectCannedACL) error {
	_, err := client.PutObjectAcl(ctx, &s3.PutObjectAclInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		ACL:    acl,
	})
	return err
}

// GetObjectAcl возвращает владельца и список грантов ACL объекта.
func GetObjectAcl(ctx context.Context, client *s3.Client, bucketName, key string) (*types.Owner, []types.Grant, error) {
	output, err := client.GetObjectAcl(ctx, &s3.GetObjectAclInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, nil, err
	}
	return output.Owner, output.Grants, nil
}
