package provider

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Общие фрагменты конфигураций и проверки для acceptance-тестов.

// testAccConfigBucket — бакет platformcraft_bucket.test с force_destroy: тесты
// с версионированием и Object Lock оставляют в бакете старые версии объектов,
// и без очистки destroy упал бы с BucketNotEmpty.
func testAccConfigBucket(bucket string) string {
	return fmt.Sprintf(`
resource "platformcraft_bucket" "test" {
  bucket        = %[1]q
  force_destroy = true
}
`, bucket)
}

// testAccConfigLockBucket — бакет с версионированием и Object Lock (GOVERNANCE).
// Object Lock требует включённого версионирования, поэтому depends_on явно.
func testAccConfigLockBucket(bucket string, days int) string {
	return testAccConfigBucket(bucket) + fmt.Sprintf(`
resource "platformcraft_bucket_versioning" "test" {
  bucket = platformcraft_bucket.test.bucket
  status = "Enabled"
}

resource "platformcraft_bucket_object_lock_configuration" "test" {
  bucket = platformcraft_bucket.test.bucket
  mode   = "GOVERNANCE"
  days   = %[1]d

  depends_on = [platformcraft_bucket_versioning.test]
}
`, days)
}

// testAccConfigLockedObject — объект hello.txt в бакете с Object Lock. Удаляется
// с bypass_governance_retention, иначе retention по умолчанию из
// testAccConfigLockBucket не даст удалить объект при destroy.
func testAccConfigLockedObject(content string) string {
	return fmt.Sprintf(`
resource "platformcraft_object" "test" {
  bucket                      = platformcraft_bucket.test.bucket
  key                         = "hello.txt"
  content                     = %[1]q
  bypass_governance_retention = true

  depends_on = [platformcraft_bucket_object_lock_configuration.test]
}
`, content)
}

// testAccCheckBucketExists проверяет через API (мимо Terraform), что бакет есть.
func testAccCheckBucketExists(t *testing.T, bucket string) func(*terraform.State) error {
	return func(*terraform.State) error {
		_, err := testAccS3Client(t).HeadBucket(context.Background(), &s3.HeadBucketInput{Bucket: aws.String(bucket)})
		if err != nil {
			return fmt.Errorf("бакет %s не найден через API: %w", bucket, err)
		}
		return nil
	}
}

// testAccCheckObjectContent скачивает объект через API и сравнивает содержимое.
func testAccCheckObjectContent(t *testing.T, bucket, key, want string) func(*terraform.State) error {
	return func(*terraform.State) error {
		out, err := testAccS3Client(t).GetObject(context.Background(), &s3.GetObjectInput{
			Bucket: aws.String(bucket), Key: aws.String(key),
		})
		if err != nil {
			return fmt.Errorf("GetObject %s/%s: %w", bucket, key, err)
		}
		defer out.Body.Close()
		got, err := io.ReadAll(out.Body)
		if err != nil {
			return err
		}
		if string(got) != want {
			return fmt.Errorf("содержимое %s/%s = %q, ожидалось %q", bucket, key, got, want)
		}
		return nil
	}
}

// testAccCheckBucketDestroyed — CheckDestroy: после destroy бакета не должно быть.
// Учитывает задержку между удалением и чтением у PlatformCraft.
func testAccCheckBucketDestroyed(t *testing.T) func(*terraform.State) error {
	return func(s *terraform.State) error {
		for _, rs := range s.RootModule().Resources {
			if rs.Type != "platformcraft_bucket" {
				continue
			}
			bucket := rs.Primary.Attributes["bucket"]
			testAccRetry(t, "ожидание удаления бакета "+bucket, func(ctx context.Context) error {
				_, err := testAccS3Client(t).HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
				if err == nil {
					return fmt.Errorf("бакет %s всё ещё существует после destroy", bucket)
				}
				if isNotFound(err) {
					return nil
				}
				return err
			})
		}
		return nil
	}
}
