# Legal hold доступен для объектов в бакете с включённым Object Lock.
resource "platformcraft_object_legal_hold" "example" {
  bucket  = "my-company-archive"
  key     = "contracts/2026/agreement.pdf"
  enabled = true
}
