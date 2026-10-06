package pcs3

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestCopySourceHeader(t *testing.T) {
	cases := []struct {
		bucket, key, want string
	}{
		{"my-bucket", "hello.txt", "my-bucket/hello.txt"},
		{"my-bucket", "dir/sub/file.txt", "my-bucket/dir/sub/file.txt"},
		{"my-bucket", "with space.txt", "my-bucket/with%20space.txt"},
		{"my-bucket", "a+b=c.txt", "my-bucket/a%2Bb=c.txt"},
		{"my-bucket", "100%.txt", "my-bucket/100%25.txt"},
		{"my-bucket", "q?.txt", "my-bucket/q%3F.txt"},
		{"my-bucket", "hash#1.txt", "my-bucket/hash%231.txt"},
		{"my-bucket", "папка/файл.txt", "my-bucket/%D0%BF%D0%B0%D0%BF%D0%BA%D0%B0/%D1%84%D0%B0%D0%B9%D0%BB.txt"},
	}
	for _, c := range cases {
		if got := copySourceHeader(c.bucket, c.key); got != c.want {
			t.Errorf("copySourceHeader(%q, %q) = %q, want %q", c.bucket, c.key, got, c.want)
		}
	}
}

// TestPresign_NoXID: в presigned URL нет параметра x-id (PlatformCraft с ним
// отклоняет подпись), остальные параметры подписи на месте.
func TestPresign_NoXID(t *testing.T) {
	client := s3.New(s3.Options{
		Region:       "eu-central-2",
		BaseEndpoint: aws.String("https://eu-s3.platformcraft.com"),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider("ak", "sk", ""),
	})
	for name, presign := range map[string]func() (string, error){
		"GET": func() (string, error) {
			return PresignGetObject(context.Background(), client, "b", "dir/file.txt", 5*time.Minute)
		},
		"PUT": func() (string, error) {
			return PresignPutObject(context.Background(), client, "b", "dir/file.txt", 5*time.Minute)
		},
	} {
		raw, err := presign()
		if err != nil {
			t.Fatalf("%s: %s", name, err)
		}
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%s: %s", name, err)
		}
		q := u.Query()
		if q.Has("x-id") {
			t.Errorf("%s: в presigned URL остался x-id: %s", name, raw)
		}
		for _, p := range []string{"X-Amz-Algorithm", "X-Amz-Credential", "X-Amz-Date", "X-Amz-Expires", "X-Amz-SignedHeaders", "X-Amz-Signature"} {
			if !q.Has(p) {
				t.Errorf("%s: нет параметра %s: %s", name, p, raw)
			}
		}
		if !strings.HasSuffix(u.Path, "/b/dir/file.txt") {
			t.Errorf("%s: неожиданный путь %s", name, u.Path)
		}
	}
}
