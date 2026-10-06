package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Data source'ы уровня объектов. Как и для бакетов — два шага: ресурсы, затем
// ресурсы + data source.

// testAccConfigTwoObjects — бакет и два объекта: a.txt (3 байта) и dir/b.txt (4 байта).
func testAccConfigTwoObjects(bucketName string) string {
	return testAccConfigBucket(bucketName) + `
resource "platformcraft_object" "a" {
  bucket  = platformcraft_bucket.test.bucket
  key     = "a.txt"
  content = "aaa"
}

resource "platformcraft_object" "b" {
  bucket  = platformcraft_bucket.test.bucket
  key     = "dir/b.txt"
  content = "bbbb"
}
`
}

func TestAccBucketObjectsDataSource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)
	base := testAccConfigTwoObjects(bucketName)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: base},
			{
				Config: base + `
data "platformcraft_bucket_objects" "test" {
  bucket     = platformcraft_bucket.test.bucket
  depends_on = [platformcraft_object.a, platformcraft_object.b]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.platformcraft_bucket_objects.test", "id", bucketName),
					resource.TestCheckResourceAttr("data.platformcraft_bucket_objects.test", "objects.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("data.platformcraft_bucket_objects.test", "objects.*", map[string]string{
						"key": "a.txt", "size": "3",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("data.platformcraft_bucket_objects.test", "objects.*", map[string]string{
						"key": "dir/b.txt", "size": "4",
					}),
				),
			},
		},
	})
}

func TestAccBucketObjectsV1DataSource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)
	base := testAccConfigTwoObjects(bucketName)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: base},
			{
				Config: base + `
data "platformcraft_bucket_objects_v1" "test" {
  bucket     = platformcraft_bucket.test.bucket
  depends_on = [platformcraft_object.a, platformcraft_object.b]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.platformcraft_bucket_objects_v1.test", "id", bucketName),
					resource.TestCheckResourceAttr("data.platformcraft_bucket_objects_v1.test", "objects.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("data.platformcraft_bucket_objects_v1.test", "objects.*", map[string]string{
						"key": "a.txt", "size": "3",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("data.platformcraft_bucket_objects_v1.test", "objects.*", map[string]string{
						"key": "dir/b.txt", "size": "4",
					}),
				),
			},
		},
	})
}

// TestAccObjectDataSource_basic: метаданные, read_content и download_path.
func TestAccObjectDataSource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)
	base := testAccConfigTwoObjects(bucketName)
	downloadPath := filepath.Join(t.TempDir(), "downloaded.txt")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: base},
			{
				Config: base + fmt.Sprintf(`
data "platformcraft_object" "meta" {
  bucket     = platformcraft_bucket.test.bucket
  key        = "a.txt"
  depends_on = [platformcraft_object.a]
}

data "platformcraft_object" "content" {
  bucket       = platformcraft_bucket.test.bucket
  key          = "dir/b.txt"
  read_content = true
  depends_on   = [platformcraft_object.b]
}

data "platformcraft_object" "download" {
  bucket        = platformcraft_bucket.test.bucket
  key           = "dir/b.txt"
  download_path = %[1]q
  depends_on    = [platformcraft_object.b]
}
`, filepath.ToSlash(downloadPath)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.platformcraft_object.meta", "id", bucketName+"/a.txt"),
					resource.TestCheckResourceAttr("data.platformcraft_object.meta", "content_length", "3"),
					resource.TestCheckResourceAttr("data.platformcraft_object.meta", "content", ""),
					resource.TestCheckResourceAttrSet("data.platformcraft_object.meta", "last_modified"),
					resource.TestCheckResourceAttrPair("data.platformcraft_object.meta", "etag", "platformcraft_object.a", "etag"),
					resource.TestCheckResourceAttr("data.platformcraft_object.content", "content", "bbbb"),
					resource.TestCheckResourceAttr("data.platformcraft_object.content", "content_length", "4"),
					func(*terraform.State) error {
						got, err := os.ReadFile(downloadPath)
						if err != nil {
							return fmt.Errorf("download_path не создан: %w", err)
						}
						if string(got) != "bbbb" {
							return fmt.Errorf("download_path содержит %q, ожидалось %q", got, "bbbb")
						}
						return nil
					},
				),
			},
		},
	})
}

// TestAccObjectDataSource_notFound: несуществующий ключ — понятная ошибка.
func TestAccObjectDataSource_notFound(t *testing.T) {
	bucketName := testAccBucketName(t)
	base := testAccConfigBucket(bucketName)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: base},
			{
				Config: base + `
data "platformcraft_object" "missing" {
  bucket = platformcraft_bucket.test.bucket
  key    = "no-such-key.txt"
}
`,
				ExpectError: regexp.MustCompile(`Объект не найден или недоступен`),
			},
		},
	})
}

func TestAccObjectAclDataSource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)
	base := testAccObjectConfig(bucketName, "acl", "private")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: base},
			{
				Config: base + `
data "platformcraft_object_acl" "test" {
  bucket     = platformcraft_object.test.bucket
  key        = platformcraft_object.test.key
  depends_on = [platformcraft_object.test]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.platformcraft_object_acl.test", "id", bucketName+"/hello.txt"),
					resource.TestCheckResourceAttrSet("data.platformcraft_object_acl.test", "owner_id"),
				),
			},
		},
	})
}

// TestAccObjectVersionsDataSource_basic: в бакете с версионированием объект
// перезаписывается, и data source видит обе версии, ровно одна из них — последняя.
func TestAccObjectVersionsDataSource_basic(t *testing.T) {
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
		Steps: []resource.TestStep{
			{Config: withObject("v1")},
			{Config: withObject("v2")},
			{
				Config: withObject("v2") + `
data "platformcraft_object_versions" "test" {
  bucket     = platformcraft_bucket.test.bucket
  depends_on = [platformcraft_object.test]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.platformcraft_object_versions.test", "id", bucketName),
					resource.TestCheckResourceAttrWith("data.platformcraft_object_versions.test", "version.#", func(v string) error {
						n, err := strconv.Atoi(v)
						if err != nil || n < 2 {
							return fmt.Errorf("ожидалось минимум 2 версии hello.txt, получено %s", v)
						}
						return nil
					}),
					resource.TestCheckTypeSetElemNestedAttrs("data.platformcraft_object_versions.test", "version.*", map[string]string{
						"key": "hello.txt", "is_latest": "true", "size": "2",
					}),
				),
			},
		},
	})
}
