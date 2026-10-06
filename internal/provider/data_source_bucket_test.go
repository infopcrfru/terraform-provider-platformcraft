package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Data source'ы уровня бакета. Каждый тест в два шага: сначала создаются
// ресурсы, затем добавляется data source. Так data source читает уже
// существующие настройки и не зависит от задержки между записью и чтением.

func TestAccBucketsDataSource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)
	base := testAccConfigBucket(bucketName)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: base},
			{
				Config: base + `
data "platformcraft_buckets" "all" {
  depends_on = [platformcraft_bucket.test]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.platformcraft_buckets.all", "id", "platformcraft_buckets"),
					resource.TestCheckTypeSetElemAttr("data.platformcraft_buckets.all", "buckets.*", bucketName),
				),
			},
		},
	})
}

func TestAccBucketDataSource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)
	base := testAccConfigBucket(bucketName) + `
resource "platformcraft_bucket_versioning" "test" {
  bucket = platformcraft_bucket.test.bucket
  status = "Enabled"
}

resource "platformcraft_object" "a" {
  bucket     = platformcraft_bucket.test.bucket
  key        = "a.txt"
  content    = "aaa"
  depends_on = [platformcraft_bucket_versioning.test]
}

resource "platformcraft_object" "b" {
  bucket     = platformcraft_bucket.test.bucket
  key        = "dir/b.txt"
  content    = "bbbb"
  depends_on = [platformcraft_bucket_versioning.test]
}
`

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: base},
			{
				Config: base + `
data "platformcraft_bucket" "test" {
  bucket     = platformcraft_bucket.test.bucket
  depends_on = [platformcraft_object.a, platformcraft_object.b]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.platformcraft_bucket.test", "id", bucketName),
					resource.TestCheckResourceAttr("data.platformcraft_bucket.test", "versioning_status", "Enabled"),
					resource.TestCheckResourceAttr("data.platformcraft_bucket.test", "object_count", "2"),
					resource.TestCheckResourceAttr("data.platformcraft_bucket.test", "total_size_bytes", "7"),
				),
			},
		},
	})
}

// TestAccBucketDataSource_notFound: несуществующий бакет — понятная ошибка, а не паника.
func TestAccBucketDataSource_notFound(t *testing.T) {
	bucketName := testAccBucketName(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
data "platformcraft_bucket" "missing" {
  bucket = %[1]q
}
`, bucketName),
				ExpectError: regexp.MustCompile(`Бакет не найден или недоступен`),
			},
		},
	})
}

func TestAccBucketVersioningDataSource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)
	base := testAccBucketVersioningConfig(bucketName, "Enabled")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: base},
			{
				Config: base + `
data "platformcraft_bucket_versioning" "test" {
  bucket     = platformcraft_bucket.test.bucket
  depends_on = [platformcraft_bucket_versioning.test]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.platformcraft_bucket_versioning.test", "id", bucketName),
					resource.TestCheckResourceAttr("data.platformcraft_bucket_versioning.test", "status", "Enabled"),
				),
			},
		},
	})
}

func TestAccBucketAclDataSource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)
	base := testAccBucketAclConfig(bucketName, "private")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: base},
			{
				Config: base + `
data "platformcraft_bucket_acl" "test" {
  bucket     = platformcraft_bucket.test.bucket
  depends_on = [platformcraft_bucket_acl.test]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.platformcraft_bucket_acl.test", "id", bucketName),
					resource.TestCheckResourceAttrSet("data.platformcraft_bucket_acl.test", "owner_id"),
				),
			},
		},
	})
}

func TestAccBucketPolicyDataSource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)
	base := testAccBucketPolicyConfig(bucketName, "DataSourceCheck")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: base},
			{
				Config: base + `
data "platformcraft_bucket_policy" "test" {
  bucket     = platformcraft_bucket.test.bucket
  depends_on = [platformcraft_bucket_policy.test]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.platformcraft_bucket_policy.test", "id", bucketName),
					resource.TestCheckResourceAttrWith("data.platformcraft_bucket_policy.test", "policy", func(v string) error {
						if !strings.Contains(v, "DataSourceCheck") || !strings.Contains(v, "s3:GetObject") {
							return fmt.Errorf("политика из API не содержит ожидаемых Sid/Action: %s", v)
						}
						return nil
					}),
				),
			},
		},
	})
}

func TestAccBucketCorsDataSource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)
	base := testAccBucketCorsConfig(bucketName, 3000)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: base},
			{
				Config: base + `
data "platformcraft_bucket_cors" "test" {
  bucket     = platformcraft_bucket.test.bucket
  depends_on = [platformcraft_bucket_cors.test]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.platformcraft_bucket_cors.test", "id", bucketName),
					resource.TestCheckResourceAttr("data.platformcraft_bucket_cors.test", "rule.#", "2"),
					resource.TestCheckResourceAttr("data.platformcraft_bucket_cors.test", "rule.0.max_age_seconds", "3000"),
					resource.TestCheckResourceAttr("data.platformcraft_bucket_cors.test", "rule.1.allowed_origins.0", "https://admin.example.com"),
				),
			},
		},
	})
}

func TestAccBucketObjectLockConfigurationDataSource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)
	base := testAccConfigLockBucket(bucketName, 1)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: base},
			{
				Config: base + `
data "platformcraft_bucket_object_lock_configuration" "test" {
  bucket     = platformcraft_bucket.test.bucket
  depends_on = [platformcraft_bucket_object_lock_configuration.test]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.platformcraft_bucket_object_lock_configuration.test", "id", bucketName),
					resource.TestCheckResourceAttr("data.platformcraft_bucket_object_lock_configuration.test", "mode", "GOVERNANCE"),
					resource.TestCheckResourceAttr("data.platformcraft_bucket_object_lock_configuration.test", "days", "1"),
				),
			},
		},
	})
}
