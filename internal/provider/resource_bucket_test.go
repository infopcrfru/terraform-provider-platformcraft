package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccBucketResource_basic: создание бакета, проверка через API, импорт по
// имени бакета и проверка, что после destroy бакета действительно нет.
func TestAccBucketResource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)
	config := fmt.Sprintf(`
resource "platformcraft_bucket" "test" {
  bucket = %[1]q
}
`, bucketName)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroyed(t),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("platformcraft_bucket.test", "bucket", bucketName),
					resource.TestCheckResourceAttr("platformcraft_bucket.test", "force_destroy", "false"),
					resource.TestCheckResourceAttr("platformcraft_bucket.test", "id", bucketName),
					testAccCheckBucketExists(t, bucketName),
				),
			},
			{
				ResourceName:      "platformcraft_bucket.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// TestAccBucketResource_disappears: бакет удалён мимо Terraform — следующий
// plan должен предложить создать его заново, а не упасть с ошибкой.
func TestAccBucketResource_disappears(t *testing.T) {
	bucketName := testAccBucketName(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccConfigBucket(bucketName),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckBucketExists(t, bucketName),
					func(*terraform.State) error {
						testAccForceDeleteBucket(t, bucketName)
						// Ждём, пока удаление станет видно через API, иначе
						// следующий plan может ещё увидеть бакет.
						testAccRetry(t, "ожидание удаления бакета", func(ctx context.Context) error {
							_, err := testAccS3Client(t).HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucketName)})
							if isNotFound(err) {
								return nil
							}
							return fmt.Errorf("бакет всё ещё виден: %v", err)
						})
						return nil
					},
				),
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAccBucketResource_forceDestroy: в бакете с версионированием остаются
// объект, его старая версия и объект, загруженный мимо Terraform. С
// force_destroy = true destroy очищает бакет и удаляет его.
func TestAccBucketResource_forceDestroy(t *testing.T) {
	bucketName := testAccBucketName(t)
	withObject := func(content string) string {
		return testAccBucketVersioningConfig(bucketName, "Enabled") + fmt.Sprintf(`
resource "platformcraft_object" "test" {
  bucket     = platformcraft_bucket.test.bucket
  key        = "hello.txt"
  content    = %[1]q
  depends_on = [platformcraft_bucket_versioning.test]
}
`, content)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroyed(t),
		Steps: []resource.TestStep{
			{Config: withObject("v1")},
			{
				Config: withObject("v2"),
				Check: func(*terraform.State) error {
					_, err := testAccS3Client(t).PutObject(context.Background(), &s3.PutObjectInput{
						Bucket: aws.String(bucketName), Key: aws.String("outside/terraform.txt"), Body: strings.NewReader("x"),
					})
					return err
				},
			},
		},
	})
}
