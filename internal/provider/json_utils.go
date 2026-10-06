package provider

import (
	"encoding/json"
	"strings"
)

// normalizeJSON приводит JSON-строку к каноническому виду (парсит и заново
// сериализует) для сравнения по смыслу, а не по форматированию/порядку ключей.
// Нужна в первую очередь в bucket_policy: сравнивать сырые строки политики
// напрямую нельзя — PlatformCraft возвращает JSON в своём форматировании
// (иной порядок ключей и т.д.), из-за чего Terraform увидит "дрифт" на каждом
// plan, хотя политика по сути не менялась. encoding/json сам сортирует ключи
// map при Marshal, так что порядок ключей эта функция уже нейтрализует; для
// специфичных для IAM-policy эквивалентностей (Principal: "*" vs {"AWS":["*"]}
// и т.п.) этого недостаточно — см. canonicalizePolicyDocument и jsonPolicyEqual.
func normalizeJSON(s string) (string, error) {
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return "", err
	}
	normalized, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(normalized), nil
}

// jsonEqual сравнивает две JSON-строки по значению, а не побайтово.
// Если хотя бы одна не парсится — считаем их разными (пусть Terraform покажет
// diff явно, чем молча спрятать некорректный JSON).
func jsonEqual(a, b string) bool {
	na, errA := normalizeJSON(a)
	if errA != nil {
		return false
	}
	nb, errB := normalizeJSON(b)
	if errB != nil {
		return false
	}
	return na == nb
}

// jsonPolicyEqual — то же самое, что jsonEqual, но для документов bucket policy
// (IAM-подобный JSON: Version/Statement/Principal/Action/Resource/...). Кроме
// форматирования и порядка ключей учитывает эквивалентности языка IAM:
// Principal "*" и {"AWS": ["*"]} (PlatformCraft возвращает политику во второй
// форме), одиночное значение и массив из одного элемента для
// Action/Resource/NotAction/NotResource/Principal.*.
func jsonPolicyEqual(a, b string) bool {
	var va, vb interface{}
	if err := json.Unmarshal([]byte(a), &va); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &vb); err != nil {
		return false
	}

	na, err := json.Marshal(canonicalizePolicyDocument(va))
	if err != nil {
		return false
	}
	nb, err := json.Marshal(canonicalizePolicyDocument(vb))
	if err != nil {
		return false
	}
	return string(na) == string(nb)
}

// canonicalizePolicyDocument приводит документ bucket policy к канонической
// форме для сравнения: везде, где IAM-грамматика допускает и одиночное
// значение, и массив из одного элемента (Principal.AWS/Service/Federated/
// CanonicalUser, Action, NotAction, Resource, NotResource), приводим к массиву;
// Principal целиком равный строке "*" ("кто угодно") приводим к
// {"AWS": ["*"]} — в этой форме политику возвращает PlatformCraft.
// Sid и другие строковые поля не трогаем — там IAM не допускает массив.
func canonicalizePolicyDocument(v interface{}) interface{} {
	doc, ok := v.(map[string]interface{})
	if !ok {
		return v
	}

	statements, ok := doc["Statement"]
	if !ok {
		return doc
	}

	switch s := statements.(type) {
	case []interface{}:
		for i, stmt := range s {
			if stmtMap, ok := stmt.(map[string]interface{}); ok {
				s[i] = canonicalizeStatement(stmtMap)
			}
		}
	case map[string]interface{}:
		doc["Statement"] = canonicalizeStatement(s)
	}

	return doc
}

func canonicalizeStatement(stmt map[string]interface{}) map[string]interface{} {
	for _, key := range []string{"Action", "NotAction", "Resource", "NotResource"} {
		if val, ok := stmt[key]; ok {
			stmt[key] = canonicalizeScalarOrArray(val)
		}
	}

	for _, key := range []string{"Principal", "NotPrincipal"} {
		val, ok := stmt[key]
		if !ok {
			continue
		}
		// "*" целиком (не внутри объекта) означает "кто угодно"; PlatformCraft
		// возвращает это как {"AWS": ["*"]}, приводим обе формы к одному виду.
		if str, ok := val.(string); ok && str == "*" {
			stmt[key] = map[string]interface{}{"AWS": []interface{}{"*"}}
			continue
		}
		if principalMap, ok := val.(map[string]interface{}); ok {
			for pk, pv := range principalMap {
				principalMap[pk] = canonicalizeScalarOrArray(pv)
			}
			stmt[key] = principalMap
		}
	}

	return stmt
}

// canonicalizeScalarOrArray оборачивает одиночное значение в массив из одного
// элемента; массив оставляет как есть.
func canonicalizeScalarOrArray(v interface{}) interface{} {
	if _, isArray := v.([]interface{}); isArray {
		return v
	}
	return []interface{}{v}
}

// splitTwo разбивает import ID вида "<bucket>,<key>" ровно на две непустые части.
// Через запятую, а не через "/" — ключ объекта сам может содержать слэши
// ("folder/subfolder/file.txt"), и SplitN по "/" неоднозначно разрезал бы такой ID.
// Возвращает nil, если формат некорректен.
func splitTwo(id string) []string {
	parts := strings.SplitN(id, ",", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil
	}
	return parts
}
