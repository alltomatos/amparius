package finance

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type TitleListFilter struct {
	Status, Direction string
	DueFrom, DueTo    *time.Time
	After             *TitleListPosition
	Limit             int
}

type TitleListPosition struct {
	DueDate time.Time
	ID      uuid.UUID
}

const titleDateLayout = "2006-01-02"

func (p TitleListPosition) encode() string {
	return base64.RawURLEncoding.EncodeToString([]byte(p.DueDate.Format(titleDateLayout) + "/" + p.ID.String()))
}

func decodeTitleListPosition(raw string) (*TitleListPosition, error) {
	const payloadLength = len(titleDateLayout) + 1 + 36
	invalid := errors.New("cursor inválido")
	if len(raw) != base64.RawURLEncoding.EncodedLen(payloadLength) {
		return nil, invalid
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return nil, invalid
	}
	dateText, idText, ok := strings.Cut(string(data), "/")
	date, dateErr := time.Parse(titleDateLayout, dateText)
	id, idErr := uuid.Parse(idText)
	if !ok || dateErr != nil || idErr != nil || id == uuid.Nil {
		return nil, invalid
	}
	return &TitleListPosition{DueDate: date, ID: id}, nil
}

func parseTitleListFilter(rawQuery string) (TitleListFilter, error) {
	var filter TitleListFilter
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return filter, errors.New("parâmetros de listagem inválidos")
	}
	for key, values := range query {
		if len(values) != 1 {
			return filter, errors.New("cada parâmetro pode ser informado uma única vez")
		}
		raw := values[0]
		switch key {
		case "limit":
			if raw = strings.TrimSpace(raw); raw != "" {
				filter.Limit, err = strconv.Atoi(raw)
				if err != nil || filter.Limit <= 0 {
					return filter, errors.New("limit deve ser inteiro positivo")
				}
			}
		case "status":
			switch raw {
			case "open", "partially_paid", "paid", "cancelled":
				filter.Status = raw
			default:
				return filter, errors.New("status inválido")
			}
		case "direction":
			switch raw {
			case "receivable", "payable":
				filter.Direction = raw
			default:
				return filter, errors.New("direction inválido")
			}
		case "due_from", "due_to":
			date, err := time.Parse(titleDateLayout, raw)
			if err != nil || date.Year() < 1 {
				return filter, fmt.Errorf("%s deve estar no formato AAAA-MM-DD", key)
			}
			if key == "due_from" {
				filter.DueFrom = &date
			} else {
				filter.DueTo = &date
			}
		case "cursor":
			filter.After, err = decodeTitleListPosition(raw)
			if err != nil {
				return filter, err
			}
		default:
			return filter, errors.New("parâmetro de listagem não reconhecido")
		}
	}
	if filter.DueFrom != nil && filter.DueTo != nil && filter.DueFrom.After(*filter.DueTo) {
		return filter, errors.New("due_from deve ser anterior ou igual a due_to")
	}
	return filter, nil
}
