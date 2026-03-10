package database

import (
	"encoding/binary"
	"errors"
)

var (
	ErrInvalidFormat = errors.New("invalid protocol format")
	ErrTooShort      = errors.New("data too short")
)

const (
	msgTypeQuery           = 'Q'
	msgTypeParse           = 'P'
	msgTypeBind            = 'B'
	msgTypeExecute         = 'E'
	msgTypeRowDesc         = 'T'
	msgTypeDataRow         = 'D'
	msgTypeCommandComplete = 'C'
	msgTypeReadyForQuery   = 'Z'
	msgTypeErrorResponse   = 'E'
	msgTypeNoticeResponse  = 'N'
)

type PGMessage struct {
	Type   byte
	Length int32
	Data   []byte
}

func ParsePGMessage(data []byte) (*PGMessage, error) {
	if len(data) < 5 {
		return nil, ErrTooShort
	}

	msg := &PGMessage{
		Type:   data[0],
		Length: int32(binary.BigEndian.Uint32(data[1:5])),
	}

	if int(msg.Length)+1 <= len(data) {
		msg.Data = data[5 : msg.Length+1]
	}

	return msg, nil
}

func ExtractQuery(data []byte) string {
	if len(data) < 1 {
		return ""
	}

	msgType := data[0]

	switch msgType {
	case msgTypeQuery:
		if len(data) < 5 {
			return ""
		}
		query := data[5 : len(data)-1]
		return string(query)

	case msgTypeParse:
		if len(data) < 6 {
			return ""
		}
		pos := 5
		for pos < len(data) && data[pos] != 0 {
			pos++
		}
		pos++
		if pos < len(data) {
			return string(data[pos : len(data)-1])
		}

	case msgTypeBind, msgTypeExecute:
		return "[bound query]"
	}

	return ""
}

func IsErrorResponse(data []byte) (bool, string) {
	if len(data) < 1 {
		return false, ""
	}

	if data[0] == msgTypeErrorResponse || data[0] == msgTypeNoticeResponse {
		var msg string
		pos := 1
		for pos < len(data)-1 {
			fieldType := data[pos]
			if fieldType == 0 {
				break
			}
			pos++
			start := pos
			for pos < len(data) && data[pos] != 0 {
				pos++
			}
			value := string(data[start:pos])

			if fieldType == 'M' || fieldType == 'C' {
				msg += value + " "
			}
			pos++
		}
		return true, msg
	}

	return false, ""
}

func ParseQueryBatch(data []byte) []string {
	var queries []string

	for len(data) > 4 {
		if data[0] != msgTypeQuery {
			break
		}

		length := int32(binary.BigEndian.Uint32(data[1:5]))
		if length+1 > int32(len(data)) {
			break
		}

		query := string(data[5 : length+1])
		queries = append(queries, query)

		data = data[length+1:]
	}

	return queries
}

func DetectPostgres(data []byte) bool {
	if len(data) < 1 {
		return false
	}
	return data[0] == msgTypeQuery ||
		data[0] == msgTypeParse ||
		data[0] == msgTypeBind ||
		data[0] == msgTypeExecute
}

func DetectMySQL(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	length := int32(binary.BigEndian.Uint32(data[:4]))
	if length+4 != int32(len(data)) {
		return false
	}
	return data[4] == 0x01 || data[4] == 0x02 || data[4] == 0x03
}

func ExtractMySQLQuery(data []byte) string {
	if len(data) < 5 {
		return ""
	}
	return string(data[5:])
}
