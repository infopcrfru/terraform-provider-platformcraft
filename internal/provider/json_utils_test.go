package provider

import "testing"

// Модульные тесты для json_utils.go — чистая логика, без сети и без реальных
// кредов PlatformCraft, поэтому гоняются в CI всегда, в отличие от
// acceptance-тестов (*_test.go с TestAcc-префиксом, требуют TF_ACC=1 и
// реальный аккаунт — см. TESTING.md "Автотесты").

func TestJsonPolicyEqual_PrincipalWildcardEquivalence(t *testing.T) {
	// PlatformCraft возвращает Principal: "*" в форме {"AWS": ["*"]} —
	// структурно другой JSON с тем же смыслом.
	a := `{"Version":"2012-10-17","Statement":[{"Sid":"PublicReadOnly","Effect":"Allow","Principal":"*","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::b/public/*"]}]}`
	b := `{"Version":"2012-10-17","Statement":[{"Sid":"PublicReadOnly","Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::b/public/*"]}]}`

	if !jsonPolicyEqual(a, b) {
		t.Errorf("jsonPolicyEqual должен считать Principal: \"*\" и Principal: {\"AWS\":[\"*\"]} эквивалентными, получили false")
	}
}

func TestJsonPolicyEqual_PrincipalAWSScalarVsArray(t *testing.T) {
	a := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"*"},"Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}]}`
	b := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::b/*"]}]}`

	if !jsonPolicyEqual(a, b) {
		t.Errorf("jsonPolicyEqual должен считать одиночное значение и массив из одного элемента эквивалентными (Principal.AWS, Action, Resource), получили false")
	}
}

func TestJsonPolicyEqual_RealDifference(t *testing.T) {
	a := `{"Version":"2012-10-17","Statement":[{"Sid":"A","Effect":"Allow","Principal":"*","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::b/*"]}]}`
	b := `{"Version":"2012-10-17","Statement":[{"Sid":"B","Effect":"Allow","Principal":"*","Action":["s3:ListBucket"],"Resource":["arn:aws:s3:::b"]}]}`

	if jsonPolicyEqual(a, b) {
		t.Errorf("jsonPolicyEqual не должен маскировать реальное различие в Action/Resource, получили true")
	}
}

func TestJsonPolicyEqual_FormattingOnlyDifference(t *testing.T) {
	// Тот же документ, другое форматирование/порядок полей — это самое базовое,
	// что должно работать (без этого сравнение бесполезно на каждый plan).
	a := `{"Version":"2012-10-17","Statement":[{"Sid":"X","Effect":"Allow","Principal":"*","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::b/*"]}]}`
	b := "{\n  \"Statement\": [\n    {\n      \"Resource\": [\"arn:aws:s3:::b/*\"],\n      \"Action\": [\"s3:GetObject\"],\n      \"Principal\": \"*\",\n      \"Effect\": \"Allow\",\n      \"Sid\": \"X\"\n    }\n  ],\n  \"Version\": \"2012-10-17\"\n}"

	if !jsonPolicyEqual(a, b) {
		t.Errorf("jsonPolicyEqual должен игнорировать форматирование и порядок ключей, получили false")
	}
}

func TestJsonPolicyEqual_InvalidJSON(t *testing.T) {
	if jsonPolicyEqual(`{not json`, `{"Version":"2012-10-17"}`) {
		t.Errorf("jsonPolicyEqual с невалидным JSON на любой стороне должен вернуть false, а не запаниковать/вернуть true")
	}
}

func TestJsonEqual_FormattingOnly(t *testing.T) {
	a := `{"a":1,"b":2}`
	b := "{\n  \"b\": 2,\n  \"a\": 1\n}"
	if !jsonEqual(a, b) {
		t.Errorf("jsonEqual должен игнорировать порядок ключей/форматирование, получили false")
	}
}

func TestSplitTwo(t *testing.T) {
	cases := []struct {
		name   string
		id     string
		wantOK bool
		wantA  string
		wantB  string
	}{
		{"simple", "bucket,key.txt", true, "bucket", "key.txt"},
		{"key with slashes", "bucket,folder/subfolder/file.txt", true, "bucket", "folder/subfolder/file.txt"},
		{"no comma", "bucketkey", false, "", ""},
		{"empty bucket", ",key.txt", false, "", ""},
		{"empty key", "bucket,", false, "", ""},
		{"empty string", "", false, "", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitTwo(c.id)
			if c.wantOK {
				if got == nil {
					t.Fatalf("splitTwo(%q) = nil, ожидали [%q, %q]", c.id, c.wantA, c.wantB)
				}
				if got[0] != c.wantA || got[1] != c.wantB {
					t.Errorf("splitTwo(%q) = [%q, %q], ожидали [%q, %q]", c.id, got[0], got[1], c.wantA, c.wantB)
				}
			} else if got != nil {
				t.Errorf("splitTwo(%q) = %v, ожидали nil (некорректный формат)", c.id, got)
			}
		})
	}
}
