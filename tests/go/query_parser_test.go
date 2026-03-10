package integration_test

import (
	"encoding/hex"
	"testing"

	db "github.com/yourorg/lang-ango/pkg/protocol/database"
)

func TestQueryParser_PostgreSQL_Select(t *testing.T) {
	qp := db.NewQueryParser()

	rawQuery := []byte("SELECT id, name FROM users WHERE id = 1")
	payload := append([]byte{byte('Q')}, 0, 0, 0, byte(len(rawQuery)+1))
	payload = append(payload, rawQuery...)
	payload = append(payload, 0)

	result, err := qp.ParsePostgres(payload)
	if err != nil {
		t.Fatalf("ParsePostgres failed: %v", err)
	}

	if result.Type != db.QueryTypeSelect {
		t.Errorf("Expected QueryTypeSelect, got %v", result.Type)
	}

	if result.Table != "users" {
		t.Errorf("Expected table 'users', got '%s'", result.Table)
	}

	if result.Query == "" {
		t.Error("Query should not be empty")
	}
}

func TestQueryParser_PostgreSQL_Insert(t *testing.T) {
	qp := db.NewQueryParser()

	rawQuery := []byte("INSERT INTO log.inbound_request_log (id, data) VALUES (1, 'test')")
	payload := append([]byte{byte('Q')}, 0, 0, 0, byte(len(rawQuery)+1))
	payload = append(payload, rawQuery...)
	payload = append(payload, 0)

	result, err := qp.ParsePostgres(payload)
	if err != nil {
		t.Fatalf("ParsePostgres failed: %v", err)
	}

	if result.Type != db.QueryTypeInsert {
		t.Errorf("Expected QueryTypeInsert, got %v", result.Type)
	}

	if result.Table != "log.inbound_request_log" {
		t.Errorf("Expected table 'log.inbound_request_log', got '%s'", result.Table)
	}
}

func TestQueryParser_MSSQL_TDS_Select(t *testing.T) {
	qp := db.NewQueryParser()

	tdsPacket := []byte{
		0x01, 0x00, 0x00, 0x2e, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x53, 0x45, 0x4c, 0x45, 0x43, 0x54, 0x20, 0x2a,
		0x20, 0x46, 0x52, 0x4f, 0x4d, 0x20, 0x70, 0x75,
		0x62, 0x6c, 0x69, 0x63, 0x2e, 0x75, 0x73, 0x65,
		0x72, 0x20, 0x57, 0x48, 0x45, 0x52, 0x45, 0x20,
		0x69, 0x64, 0x20, 0x3d, 0x20, 0x40, 0x49, 0x64,
	}

	result, err := qp.ParseMSSQL(tdsPacket)
	if err != nil {
		t.Fatalf("ParseMSSQL failed: %v", err)
	}

	if result.Type != db.QueryTypeSelect {
		t.Errorf("Expected QueryTypeSelect, got %v", result.Type)
	}

	if result.Query == "" {
		t.Error("Query should not be empty")
	}
}

func TestQueryParser_MSSQL_TDS_Insert(t *testing.T) {
	qp := db.NewQueryParser()

	tdsPacket := []byte{
		0x01, 0x00, 0x00, 0x40, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x49, 0x4e, 0x53, 0x45, 0x52, 0x54, 0x20, 0x49,
		0x4e, 0x54, 0x4f, 0x20, 0x6c, 0x6f, 0x67, 0x2e,
		0x69, 0x6e, 0x62, 0x6f, 0x75, 0x6e, 0x64, 0x5f,
		0x72, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x5f,
		0x6c, 0x6f, 0x67, 0x20, 0x28, 0x69, 0x64, 0x2c,
		0x20, 0x64, 0x61, 0x74, 0x61, 0x29, 0x20, 0x56,
		0x41, 0x4c, 0x55, 0x45, 0x53, 0x20, 0x28, 0x31,
		0x2c, 0x20, 0x27, 0x74, 0x65, 0x73, 0x74, 0x27,
		0x29,
	}

	result, err := qp.ParseMSSQL(tdsPacket)
	if err != nil {
		t.Fatalf("ParseMSSQL failed: %v", err)
	}

	if result.Type != db.QueryTypeInsert {
		t.Errorf("Expected QueryTypeInsert, got %v", result.Type)
	}

	if result.Table != "log.inbound_request_log" {
		t.Errorf("Expected table 'log.inbound_request_log', got '%s'", result.Table)
	}
}

func TestQueryParser_AnalyzeQuery_Select(t *testing.T) {
	qp := db.NewQueryParser()

	tests := []struct {
		name     string
		query    string
		expected db.QueryType
		table    string
	}{
		{"SELECT simple", "SELECT * FROM users", db.QueryTypeSelect, "users"},
		{"SELECT with WHERE", "SELECT id, name FROM products WHERE price > 100", db.QueryTypeSelect, "products"},
		{"SELECT with JOIN", "SELECT * FROM orders JOIN users ON orders.user_id = users.id", db.QueryTypeSelect, "orders"},
		{"SELECT with schema", "SELECT * FROM cache.sbm_tescil_sorgu_value", db.QueryTypeSelect, "cache.sbm_tescil_sorgu_value"},
		{"INSERT simple", "INSERT INTO logs (id, msg) VALUES (1, 'test')", db.QueryTypeInsert, "logs"},
		{"UPDATE simple", "UPDATE users SET name = 'new' WHERE id = 1", db.QueryTypeUpdate, "users"},
		{"DELETE simple", "DELETE FROM sessions WHERE expired = true", db.QueryTypeDelete, "sessions"},
		{"CREATE TABLE", "CREATE TABLE new_table (id INT, name VARCHAR(100))", db.QueryTypeCreate, "new_table"},
		{"DROP TABLE", "DROP TABLE old_table", db.QueryTypeDrop, "old_table"},
		{"ALTER TABLE", "ALTER TABLE users ADD COLUMN email VARCHAR(255)", db.QueryTypeAlter, "users"},
		{"EXEC stored proc", "EXEC sp_GetUserById 1", db.QueryTypeExec, ""},
		{"BEGIN transaction", "BEGIN TRANSACTION", db.QueryTypeBegin, ""},
		{"COMMIT", "COMMIT", db.QueryTypeCommit, ""},
		{"ROLLBACK", "ROLLBACK", db.QueryTypeRollback, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := qp.AnalyzeQueryForTest(tt.query)
			if result.Type != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result.Type)
			}
			if result.Table != tt.table {
				t.Errorf("Expected table '%s', got '%s'", tt.table, result.Table)
			}
		})
	}
}

func TestQueryParser_ExtractTableName(t *testing.T) {
	qp := db.NewQueryParser()

	tests := []struct {
		name     string
		query    string
		qtype    db.QueryType
		expected string
	}{
		{"SELECT FROM", "SELECT * FROM users", db.QueryTypeSelect, "users"},
		{"SELECT INTO", "SELECT * INTO temp FROM users", db.QueryTypeSelect, "temp"},
		{"SELECT JOIN", "SELECT * FROM a JOIN b ON a.id = b.a_id", db.QueryTypeSelect, "a"},
		{"INSERT INTO", "INSERT INTO logs (id) VALUES (1)", db.QueryTypeInsert, "logs"},
		{"UPDATE", "UPDATE products SET price = 10", db.QueryTypeUpdate, "products"},
		{"DELETE FROM", "DELETE FROM sessions", db.QueryTypeDelete, "sessions"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := qp.AnalyzeQueryForTest(tt.query)
			if result.Table != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result.Table)
			}
		})
	}
}

func BenchmarkQueryParser_PostgreSQL(b *testing.B) {
	qp := db.NewQueryParser()

	rawQuery := []byte("SELECT id, name FROM users WHERE id = 1")
	payload := append([]byte{byte('Q')}, 0, 0, 0, byte(len(rawQuery)+1))
	payload = append(payload, rawQuery...)
	payload = append(payload, 0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		qp.ParsePostgres(payload)
	}
}

func BenchmarkQueryParser_MSSQL(b *testing.B) {
	qp := db.NewQueryParser()

	tdsPacket := []byte{
		0x01, 0x00, 0x00, 0x2e, 0x00, 0x00, 0x00, 0x00,
		0x53, 0x45, 0x4c, 0x45, 0x43, 0x54, 0x20, 0x2a,
		0x20, 0x46, 0x52, 0x4f, 0x4d, 0x20, 0x70, 0x75,
		0x62, 0x6c, 0x69, 0x63, 0x2e, 0x75, 0x73, 0x65,
		0x72, 0x20, 0x57, 0x48, 0x45, 0x52, 0x45, 0x20,
		0x69, 0x64, 0x20, 0x3d, 0x20, 0x40, 0x49, 0x64,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		qp.ParseMSSQL(tdsPacket)
	}
}

func TestQueryTypeString(t *testing.T) {
	tests := []struct {
		input    db.QueryType
		expected string
	}{
		{db.QueryTypeSelect, "SELECT"},
		{db.QueryTypeInsert, "INSERT"},
		{db.QueryTypeUpdate, "UPDATE"},
		{db.QueryTypeDelete, "DELETE"},
		{db.QueryTypeCreate, "CREATE"},
		{db.QueryTypeDrop, "DROP"},
		{db.QueryTypeAlter, "ALTER"},
		{db.QueryTypeExec, "EXEC"},
		{db.QueryTypeBegin, "BEGIN"},
		{db.QueryTypeCommit, "COMMIT"},
		{db.QueryTypeRollback, "ROLLBACK"},
		{db.QueryTypeUnknown, "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.input.String(); got != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, got)
			}
		})
	}
}

func TestPostgresHexDump(t *testing.T) {
	qp := db.NewQueryParser()

	hexData := "510000002253454c4543542069642c206e616d652046524f4d207075626c69632e75736572205748455245206964203d203100"
	bytes, err := hex.DecodeString(hexData)
	if err != nil {
		t.Fatalf("Failed to decode hex: %v", err)
	}

	result, err := qp.ParsePostgres(bytes)
	if err != nil {
		t.Fatalf("ParsePostgres failed: %v", err)
	}

	if result.Type != db.QueryTypeSelect {
		t.Errorf("Expected QueryTypeSelect, got %v", result.Type)
	}

	if result.Table != "public.user" {
		t.Logf("Got table: %s", result.Table)
	}
}
