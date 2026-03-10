package database

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
)

type QueryType int

const (
	QueryTypeUnknown QueryType = iota
	QueryTypeSelect
	QueryTypeInsert
	QueryTypeUpdate
	QueryTypeDelete
	QueryTypeCreate
	QueryTypeDrop
	QueryTypeAlter
	QueryTypeExec
	QueryTypeBegin
	QueryTypeCommit
	QueryTypeRollback
)

type SQLQuery struct {
	Type         QueryType
	Query        string
	Table        string
	Database     string
	Parameters   []string
	DurationNS   int64
	RowsAffected int64
	Error        string
}

type QueryParser struct {
	maxQueryLen int
	obfuscate   bool
}

func NewQueryParser() *QueryParser {
	return &QueryParser{
		maxQueryLen: 4096,
		obfuscate:   false,
	}
}

func (qp *QueryParser) ParsePostgres(data []byte) (*SQLQuery, error) {
	if len(data) < 5 {
		return nil, fmt.Errorf("data too short for PostgreSQL")
	}

	msgType := data[0]
	switch msgType {
	case 'Q':
		return qp.parsePostgresQuery(data[5:])
	case 'P':
		return qp.parsePostgresParse(data[5:])
	case 'E':
		return qp.parsePostgresError(data[5:])
	default:
		return &SQLQuery{Type: QueryTypeUnknown, Query: ""}, nil
	}
}

func (qp *QueryParser) parsePostgresQuery(payload []byte) (*SQLQuery, error) {
	nullIdx := bytes.IndexByte(payload, 0)
	if nullIdx == -1 {
		return nil, fmt.Errorf("invalid PostgreSQL message")
	}

	query := string(payload[:nullIdx])
	return qp.analyzeQuery(query), nil
}

func (qp *QueryParser) parsePostgresParse(payload []byte) (*SQLQuery, error) {
	parts := bytes.Split(payload, []byte{0})
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid Parse message")
	}

	query := string(parts[1])
	return qp.analyzeQuery(query), nil
}

func (qp *QueryParser) parsePostgresError(payload []byte) (*SQLQuery, error) {
	return &SQLQuery{Type: QueryTypeUnknown, Query: ""}, nil
}

func (qp *QueryParser) ParseMSSQL(data []byte) (*SQLQuery, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("data too short for MSSQL TDS")
	}

	packetType := data[0]
	switch packetType {
	case 0x01:
		return qp.parseMSSQLBatch(data[8:])
	case 0x04:
		return qp.parseMSSQLResponse(data[8:])
	case 0xE3:
		return qp.parseMSSQLRPC(data[8:])
	default:
		return &SQLQuery{Type: QueryTypeUnknown, Query: ""}, nil
	}
}

func (qp *QueryParser) parseMSSQLBatch(payload []byte) (*SQLQuery, error) {
	if len(payload) < 8 {
		return nil, fmt.Errorf("invalid TDS batch")
	}

	tdsLength := int(payload[2])<<8 | int(payload[3])
	headerLen := 8

	// Clamp TDS length to safe bounds to avoid panics on malformed packets.
	if tdsLength < headerLen || tdsLength > len(payload) {
		tdsLength = len(payload)
	}
	if len(payload) <= headerLen {
		return &SQLQuery{Type: QueryTypeUnknown, Query: ""}, nil
	}

	queryData := payload[headerLen:tdsLength]

	query := qp.extractSQLFromTDS(queryData)
	if query == "" {
		return &SQLQuery{Type: QueryTypeUnknown, Query: ""}, nil
	}

	return qp.analyzeQuery(query), nil
}

func (qp *QueryParser) parseMSSQLRPC(payload []byte) (*SQLQuery, error) {
	if len(payload) < 2 {
		return nil, fmt.Errorf("invalid TDS RPC")
	}

	return &SQLQuery{Type: QueryTypeExec, Query: "RPC"}, nil
}

func (qp *QueryParser) parseMSSQLResponse(payload []byte) (*SQLQuery, error) {
	return &SQLQuery{Type: QueryTypeUnknown, Query: ""}, nil
}

func (qp *QueryParser) extractSQLFromTDS(data []byte) string {
	if len(data) < 4 {
		return ""
	}

	start := 0
	for i := 0; i < len(data)-4; i++ {
		if data[i] == 'S' && data[i+1] == 'E' && data[i+2] == 'L' && data[i+3] == 'E' ||
			data[i] == 'I' && data[i+1] == 'N' && data[i+2] == 'S' && data[i+3] == 'E' ||
			data[i] == 'U' && data[i+1] == 'P' && data[i+2] == 'D' && data[i+3] == 'A' ||
			data[i] == 'D' && data[i+1] == 'E' && data[i+2] == 'L' && data[i+3] == 'E' ||
			data[i] == 'E' && data[i+1] == 'X' && data[i+2] == 'E' && data[i+3] == 'C' ||
			data[i] == 'C' && data[i+1] == 'R' && data[i+2] == 'E' && data[i+3] == 'A' ||
			data[i] == 'D' && data[i+1] == 'R' && data[i+2] == 'O' && data[i+3] == 'P' ||
			data[i] == 'A' && data[i+1] == 'L' && data[i+2] == 'T' && data[i+3] == 'E' {
			start = i
			break
		}
	}

	if start == 0 {
		start = bytes.IndexFunc(data, func(r rune) bool {
			return unicode.IsLetter(r)
		})
	}

	if start == -1 {
		return ""
	}

	query := strings.TrimSpace(string(data[start:]))
	if len(query) > qp.maxQueryLen {
		query = query[:qp.maxQueryLen]
	}

	return query
}

func (qp *QueryParser) analyzeQuery(query string) *SQLQuery {
	query = strings.TrimSpace(query)
	if query == "" {
		return &SQLQuery{Type: QueryTypeUnknown, Query: ""}
	}

	upperQuery := strings.ToUpper(query)

	queryType := QueryTypeUnknown
	if strings.HasPrefix(upperQuery, "SELECT") {
		queryType = QueryTypeSelect
	} else if strings.HasPrefix(upperQuery, "INSERT") {
		queryType = QueryTypeInsert
	} else if strings.HasPrefix(upperQuery, "UPDATE") {
		queryType = QueryTypeUpdate
	} else if strings.HasPrefix(upperQuery, "DELETE") {
		queryType = QueryTypeDelete
	} else if strings.HasPrefix(upperQuery, "CREATE") {
		queryType = QueryTypeCreate
	} else if strings.HasPrefix(upperQuery, "DROP") {
		queryType = QueryTypeDrop
	} else if strings.HasPrefix(upperQuery, "ALTER") {
		queryType = QueryTypeAlter
	} else if strings.HasPrefix(upperQuery, "EXEC") || strings.HasPrefix(upperQuery, "EXECUTE") {
		queryType = QueryTypeExec
	} else if strings.HasPrefix(upperQuery, "BEGIN") {
		queryType = QueryTypeBegin
	} else if strings.HasPrefix(upperQuery, "COMMIT") {
		queryType = QueryTypeCommit
	} else if strings.HasPrefix(upperQuery, "ROLLBACK") {
		queryType = QueryTypeRollback
	}

	table := qp.extractTableName(query, queryType)

	sql := &SQLQuery{
		Type:  queryType,
		Query: query,
		Table: table,
	}

	return sql
}

// AnalyzeQueryForTest is an exported helper used by tests to verify
// query classification logic without relying on unexported methods.
func (qp *QueryParser) AnalyzeQueryForTest(query string) *SQLQuery {
	return qp.analyzeQuery(query)
}

// ExtractTableNameForTest is an exported helper used by tests to verify
// table name extraction behaviour.
func (qp *QueryParser) ExtractTableNameForTest(query string, queryType QueryType) string {
	return qp.extractTableName(query, queryType)
}

func (qp *QueryParser) extractTableName(query string, queryType QueryType) string {
	query = strings.TrimSpace(query)

	keywords := map[QueryType][]string{
		QueryTypeSelect: {"FROM", "INTO", "JOIN"},
		QueryTypeInsert: {"INTO", "TABLE"},
		QueryTypeUpdate: {"UPDATE", "SET"},
		QueryTypeDelete: {"FROM", "WHERE"},
		QueryTypeCreate: {"TABLE", "INDEX", "VIEW"},
		QueryTypeDrop:   {"TABLE", "INDEX", "VIEW"},
		QueryTypeAlter:  {"TABLE"},
	}

	searchKeywords, ok := keywords[queryType]
	if !ok {
		return ""
	}

	upperQuery := strings.ToUpper(query)

	bestIdx := -1
	bestTable := ""

	for _, keyword := range searchKeywords {
		idx := strings.Index(upperQuery, keyword)
		if idx == -1 {
			continue
		}

		afterKeyword := strings.TrimSpace(query[idx+len(keyword):])
		parts := strings.Fields(afterKeyword)
		if len(parts) == 0 {
			continue
		}

		tableName := strings.Trim(parts[0], "()[]`\"")
		if tableName == "" || strings.HasPrefix(tableName, "SELECT") {
			continue
		}

		if bestIdx == -1 || idx < bestIdx {
			bestIdx = idx
			bestTable = tableName
		}
	}

	return bestTable
}

func (qt QueryType) String() string {
	switch qt {
	case QueryTypeSelect:
		return "SELECT"
	case QueryTypeInsert:
		return "INSERT"
	case QueryTypeUpdate:
		return "UPDATE"
	case QueryTypeDelete:
		return "DELETE"
	case QueryTypeCreate:
		return "CREATE"
	case QueryTypeDrop:
		return "DROP"
	case QueryTypeAlter:
		return "ALTER"
	case QueryTypeExec:
		return "EXEC"
	case QueryTypeBegin:
		return "BEGIN"
	case QueryTypeCommit:
		return "COMMIT"
	case QueryTypeRollback:
		return "ROLLBACK"
	default:
		return "UNKNOWN"
	}
}
