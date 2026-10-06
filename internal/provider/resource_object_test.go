package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccObjectResource_basic: создание с content, изменение content, импорт
// по составному ID "<bucket>,<key>". Read не скачивает содержимое (кроме
// refresh_content = true), поэтому content после импорта пустой и не сверяется.
func TestAccObjectResource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccObjectConfig(bucketName, "hello v1", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("platformcraft_object.test", "content", "hello v1"),
					resource.TestCheckResourceAttr("platformcraft_object.test", "id", bucketName+"/hello.txt"),
					resource.TestCheckResourceAttrSet("platformcraft_object.test", "etag"),
					testAccCheckObjectContent(t, bucketName, "hello.txt", "hello v1"),
				),
			},
			{
				Config: testAccObjectConfig(bucketName, "hello v2", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("platformcraft_object.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("platformcraft_object.test", "content", "hello v2"),
					testAccCheckObjectContent(t, bucketName, "hello.txt", "hello v2"),
				),
			},
			{
				ResourceName:            "platformcraft_object.test",
				ImportState:             true,
				ImportStateId:           bucketName + ",hello.txt",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"content"},
			},
		},
	})
}

// TestAccObjectResource_acl: объект с ACL public-read, затем private.
func TestAccObjectResource_acl(t *testing.T) {
	bucketName := testAccBucketName(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccObjectConfig(bucketName, "acl test", "public-read"),
				Check:  resource.TestCheckResourceAttr("platformcraft_object.test", "acl", "public-read"),
			},
			{
				Config: testAccObjectConfig(bucketName, "acl test", "private"),
				Check:  resource.TestCheckResourceAttr("platformcraft_object.test", "acl", "private"),
			},
		},
	})
}

// TestAccObjectResource_sourcePath: загрузка из локального файла и ключ с
// пробелами, кириллицей и вложенными «папками».
func TestAccObjectResource_sourcePath(t *testing.T) {
	bucketName := testAccBucketName(t)
	src := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(src, []byte("from file"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := "папка/файл с пробелом.txt"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccConfigBucket(bucketName) + fmt.Sprintf(`
resource "platformcraft_object" "test" {
  bucket      = platformcraft_bucket.test.bucket
  key         = %[1]q
  source_path = %[2]q
}
`, key, filepath.ToSlash(src)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("platformcraft_object.test", "etag"),
					testAccCheckObjectContent(t, bucketName, key, "from file"),
				),
			},
		},
	})
}

// TestAccObjectResource_refreshContentDrift: при refresh_content = true
// изменение содержимого мимо Terraform видно в plan как update.
func TestAccObjectResource_refreshContentDrift(t *testing.T) {
	bucketName := testAccBucketName(t)
	config := testAccConfigBucket(bucketName) + `
resource "platformcraft_object" "test" {
  bucket          = platformcraft_bucket.test.bucket
  key             = "hello.txt"
  content         = "original"
  refresh_content = true
}
`

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  resource.TestCheckResourceAttr("platformcraft_object.test", "content", "original"),
			},
			{
				PreConfig: func() {
					testAccRetry(t, "перезапись объекта мимо Terraform", func(ctx context.Context) error {
						_, err := testAccS3Client(t).PutObject(ctx, &s3.PutObjectInput{
							Bucket: aws.String(bucketName), Key: aws.String("hello.txt"), Body: strings.NewReader("changed outside"),
						})
						if err != nil {
							return err
						}
						return testAccCheckObjectContent(t, bucketName, "hello.txt", "changed outside")(nil)
					})
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("platformcraft_object.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: testAccCheckObjectContent(t, bucketName, "hello.txt", "original"),
			},
		},
	})
}

// TestAccObjectResource_disappears: объект удалён мимо Terraform — plan
// предлагает создать его заново.
func TestAccObjectResource_disappears(t *testing.T) {
	bucketName := testAccBucketName(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccObjectConfig(bucketName, "hello", ""),
				Check: func(*terraform.State) error {
					client := testAccS3Client(t)
					testAccRetry(t, "удаление объекта мимо Terraform", func(ctx context.Context) error {
						if _, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucketName), Key: aws.String("hello.txt")}); err != nil {
							return err
						}
						_, err := client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucketName), Key: aws.String("hello.txt")})
						if isNotFound(err) {
							return nil
						}
						return fmt.Errorf("объект всё ещё виден после удаления: %v", err)
					})
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func testAccObjectConfig(bucketName, content, acl string) string {
	aclLine := ""
	if acl != "" {
		aclLine = fmt.Sprintf("  acl     = %q\n", acl)
	}
	return testAccConfigBucket(bucketName) + fmt.Sprintf(`
resource "platformcraft_object" "test" {
  bucket  = platformcraft_bucket.test.bucket
  key     = "hello.txt"
  content = %[1]q
%[2]s}
`, content, aclLine)
}
