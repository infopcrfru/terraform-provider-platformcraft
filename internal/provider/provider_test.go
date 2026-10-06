package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/echoprovider"

	"github.com/infopcrfru/terraform-provider-platformcraft/internal/pcs3"
)

// Acceptance-тесты (функции TestAcc*) работают с настоящим PlatformCraft API:
// создают и удаляют реальные бакеты и объекты. Запускаются только при TF_ACC=1
// (стандартный контракт terraform-plugin-testing: без него resource.Test()
// пропускает тест). Модульные тесты (json_utils_test.go, internal/pcs3/*_test.go)
// работают без сети и запускаются всегда. Как запускать — TESTING.md, раздел 5.

// testAccProtoV6ProviderFactories — провайдер для всех acceptance-тестов.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"platformcraft": providerserver.NewProtocol6WithError(New("test")()),
}

// testAccProtoV6ProviderFactoriesWithEcho добавляет служебный провайдер echo
// из terraform-plugin-testing: ephemeral-значения не попадают в state, и echo —
// штатный способ проверить их в тесте (значение передаётся в provider "echo",
// а resource "echo" возвращает его в state тестового прогона).
var testAccProtoV6ProviderFactoriesWithEcho = map[string]func() (tfprotov6.ProviderServer, error){
	"platformcraft": providerserver.NewProtocol6WithError(New("test")()),
	"echo":          echoprovider.NewProviderServer(),
}

// testAccPreCheck проверяет, что заданы ключи доступа, до создания ресурсов.
func testAccPreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("PLATFORMCRAFT_ACCESS_KEY") == "" {
		t.Fatal("для acceptance-тестов (TF_ACC=1) нужна переменная PLATFORMCRAFT_ACCESS_KEY")
	}
	if os.Getenv("PLATFORMCRAFT_SECRET_KEY") == "" {
		t.Fatal("для acceptance-тестов (TF_ACC=1) нужна переменная PLATFORMCRAFT_SECRET_KEY")
	}
}

var testAccNameCleanup = regexp.MustCompile(`[^a-z0-9-]+`)

// testAccNameSeq — счётчик для уникальности имён внутри процесса: на Windows
// time.Now() может вернуть одно и то же значение для двух вызовов подряд, и
// два бакета одного теста получили бы одинаковые имена.
var testAccNameSeq atomic.Int64

// testAccBucketName возвращает уникальное имя бакета для теста и регистрирует
// аварийную очистку этого бакета после теста (testAccForceDeleteBucket).
//
// Имена бакетов в PlatformCraft уникальны глобально, поэтому в имя входит метка
// времени. Длина не превышает 63 символа (ограничение S3 на имя бакета):
// префикс "tf-acc-", до 30 символов имени теста, метка времени и порядковый
// номер в base36.
func testAccBucketName(t *testing.T) string {
	t.Helper()
	name := strings.ToLower(t.Name())
	name = strings.TrimPrefix(name, "testacc")
	name = testAccNameCleanup.ReplaceAllString(strings.ReplaceAll(name, "_", "-"), "-")
	name = strings.Trim(name, "-")
	if len(name) > 30 {
		name = strings.Trim(name[:30], "-")
	}
	bucket := fmt.Sprintf("tf-acc-%s-%s%s", name,
		strconv.FormatInt(time.Now().UnixNano(), 36), strconv.FormatInt(testAccNameSeq.Add(1), 36))

	if os.Getenv("TF_ACC") != "" {
		t.Cleanup(func() { testAccForceDeleteBucket(t, bucket) })
	}
	return bucket
}

// testAccS3Client возвращает клиент PlatformCraft с той же конфигурацией, что
// и у провайдера (newS3Client). Нужен для подготовки дрифта (изменения мимо
// Terraform) и для аварийной очистки.
func testAccS3Client(t *testing.T) *s3.Client {
	t.Helper()
	client, err := newS3Client(context.Background(),
		envOrDefault("PLATFORMCRAFT_ENDPOINT", "https://eu-s3.platformcraft.com"),
		envOrDefault("PLATFORMCRAFT_REGION", "eu-central-2"),
		os.Getenv("PLATFORMCRAFT_ACCESS_KEY"),
		os.Getenv("PLATFORMCRAFT_SECRET_KEY"),
		testAccMaxRPS(),
	)
	if err != nil {
		t.Fatalf("не удалось создать S3-клиент для теста: %s", err)
	}
	return client
}

// testAccMaxRPS — та же частота запросов, что у провайдера: лимитер общий на
// процесс теста, в котором работает и провайдер, и этот клиент.
func testAccMaxRPS() int {
	if n, err := strconv.Atoi(os.Getenv("PLATFORMCRAFT_MAX_RPS")); err == nil && n >= 0 {
		return n
	}
	return pcs3.DefaultMaxRequestsPerSecond
}

func envOrDefault(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// testAccForceDeleteBucket удаляет бакет со всем содержимым, если он остался
// после теста (например, destroy упал на середине из-за сбоя API). В обычном
// успешном прогоне бакета к этому моменту уже нет, и функция ничего не делает.
//
// Снимает legal hold, удаляет все версии и delete-маркеры с
// BypassGovernanceRetention (тесты используют только режим GOVERNANCE), затем
// удаляет сам бакет. Ошибки не валят тест, а пишутся в лог: оставшийся бакет
// видно в выводе `go test -v`, его можно удалить вручную.
func testAccForceDeleteBucket(t *testing.T, bucket string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client := testAccS3Client(t)

	if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}); err != nil {
		if isNotFound(err) {
			return
		}
		t.Logf("очистка: HeadBucket %s: %s", bucket, err)
	}
	t.Logf("очистка: удаляем бакет %s вместе с содержимым", bucket)

	deleteVersion := func(key, versionID *string) {
		_, _ = client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
			Bucket:    aws.String(bucket),
			Key:       key,
			VersionId: versionID,
			LegalHold: &s3types.ObjectLockLegalHold{Status: s3types.ObjectLockLegalHoldStatusOff},
		})
		_, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket:                    aws.String(bucket),
			Key:                       key,
			VersionId:                 versionID,
			BypassGovernanceRetention: aws.Bool(true),
		})
		if err != nil {
			t.Logf("очистка: DeleteObject %s/%s (version %s): %s", bucket, aws.ToString(key), aws.ToString(versionID), err)
		}
	}

	var keyMarker, versionMarker *string
	for {
		out, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
			Bucket: aws.String(bucket), KeyMarker: keyMarker, VersionIdMarker: versionMarker,
		})
		if err != nil {
			t.Logf("очистка: ListObjectVersions %s: %s", bucket, err)
			break
		}
		for _, v := range out.Versions {
			deleteVersion(v.Key, v.VersionId)
		}
		for _, m := range out.DeleteMarkers {
			deleteVersion(m.Key, m.VersionId)
		}
		if out.IsTruncated == nil || !*out.IsTruncated || (out.NextKeyMarker == nil && out.NextVersionIdMarker == nil) {
			break
		}
		keyMarker, versionMarker = out.NextKeyMarker, out.NextVersionIdMarker
	}

	// На случай бакета без версионирования, где ListObjectVersions может вернуть пустой список.
	if out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)}); err == nil {
		for _, o := range out.Contents {
			deleteVersion(o.Key, nil)
		}
	}

	if _, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)}); err != nil && !isNotFound(err) {
		t.Logf("очистка: не удалось удалить бакет %s, удалите его вручную: %s", bucket, err)
	}
}

// testAccRetry повторяет действие вне Terraform (подготовка дрифта), пока оно
// не выполнится: у PlatformCraft бывают задержки между записью и чтением.
func testAccRetry(t *testing.T, desc string, fn func(ctx context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var err error
	for delay := 500 * time.Millisecond; ; delay *= 2 {
		if err = fn(ctx); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s: %s", desc, err)
		case <-time.After(delay):
		}
	}
}

// testAccRetainUntil возвращает момент в будущем в формате, который принимает
// platformcraft_object_retention (RFC3339, UTC, без долей секунды).
func testAccRetainUntil(d time.Duration) string {
	return time.Now().UTC().Add(d).Truncate(time.Second).Format(time.RFC3339)
}
