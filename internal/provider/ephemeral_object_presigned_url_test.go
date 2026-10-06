package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

// Ephemeral-значения не попадают в state, поэтому URL передаётся в служебный
// провайдер echo, а проверка делает по нему настоящий HTTP-запрос: ссылка не
// просто сгенерирована, а реально принимается API. Требуется Terraform >= 1.10.

func TestAccObjectPresignedGetUrlEphemeral_basic(t *testing.T) {
	bucketName := testAccBucketName(t)
	base := testAccObjectConfig(bucketName, "presigned get", "")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactoriesWithEcho,
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_10_0),
		},
		Steps: []resource.TestStep{
			{Config: base},
			{
				Config: base + `
ephemeral "platformcraft_object_presigned_get_url" "test" {
  bucket          = platformcraft_object.test.bucket
  key             = platformcraft_object.test.key
  expires_seconds = 300
}

provider "echo" {
  data = ephemeral.platformcraft_object_presigned_get_url.test.url
}

resource "echo" "test" {}
`,
				Check: resource.TestCheckResourceAttrWith("echo.test", "data", func(url string) error {
					resp, err := http.Get(url)
					if err != nil {
						return fmt.Errorf("GET по presigned URL: %w", err)
					}
					defer resp.Body.Close()
					body, _ := io.ReadAll(resp.Body)
					if resp.StatusCode != http.StatusOK {
						return fmt.Errorf("GET по presigned URL вернул %d: %s", resp.StatusCode, body)
					}
					if string(body) != "presigned get" {
						return fmt.Errorf("GET по presigned URL вернул %q, ожидалось %q", body, "presigned get")
					}
					return nil
				}),
			},
		},
	})
}

func TestAccObjectPresignedPutUrlEphemeral_basic(t *testing.T) {
	bucketName := testAccBucketName(t)
	base := testAccConfigBucket(bucketName)
	const key = "uploaded-via-presigned.txt"
	const payload = "uploaded through presigned PUT"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactoriesWithEcho,
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_10_0),
		},
		Steps: []resource.TestStep{
			{Config: base},
			{
				Config: base + fmt.Sprintf(`
ephemeral "platformcraft_object_presigned_put_url" "test" {
  bucket          = platformcraft_bucket.test.bucket
  key             = %[1]q
  expires_seconds = 300
}

provider "echo" {
  data = ephemeral.platformcraft_object_presigned_put_url.test.url
}

resource "echo" "test" {}
`, key),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("echo.test", "data", func(url string) error {
						req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(payload))
						if err != nil {
							return err
						}
						resp, err := http.DefaultClient.Do(req)
						if err != nil {
							return fmt.Errorf("PUT по presigned URL: %w", err)
						}
						defer resp.Body.Close()
						if resp.StatusCode != http.StatusOK {
							body, _ := io.ReadAll(resp.Body)
							return fmt.Errorf("PUT по presigned URL вернул %d: %s", resp.StatusCode, body)
						}
						return nil
					}),
					testAccCheckObjectContent(t, bucketName, key, payload),
					// Объект создан мимо Terraform — удаляем его сами, иначе
					// destroy не сможет удалить непустой бакет.
					func(*terraform.State) error {
						_, err := testAccS3Client(t).DeleteObject(context.Background(), &s3.DeleteObjectInput{
							Bucket: aws.String(bucketName), Key: aws.String(key),
						})
						return err
					},
				),
			},
		},
	})
}
