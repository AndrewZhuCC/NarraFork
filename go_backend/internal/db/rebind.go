package db

import (
	"strconv"
	"strings"
)

func rebindPostgres(query string) string {
	var out strings.Builder
	out.Grow(len(query) + 8)
	placeholder := 1
	state := rebindStateDefault
	for i := 0; i < len(query); i++ {
		ch := query[i]
		switch state {
		case rebindStateDefault:
			if ch == '\'' {
				state = rebindStateSingleQuote
				out.WriteByte(ch)
				continue
			}
			if ch == '"' {
				state = rebindStateDoubleQuote
				out.WriteByte(ch)
				continue
			}
			if ch == '-' && i+1 < len(query) && query[i+1] == '-' {
				state = rebindStateLineComment
				out.WriteString("--")
				i++
				continue
			}
			if ch == '/' && i+1 < len(query) && query[i+1] == '*' {
				state = rebindStateBlockComment
				out.WriteString("/*")
				i++
				continue
			}
			if ch == '?' {
				out.WriteByte('$')
				out.WriteString(strconv.Itoa(placeholder))
				placeholder++
				continue
			}
			out.WriteByte(ch)
		case rebindStateSingleQuote:
			out.WriteByte(ch)
			if ch == '\'' {
				if i+1 < len(query) && query[i+1] == '\'' {
					out.WriteByte(query[i+1])
					i++
					continue
				}
				state = rebindStateDefault
			}
		case rebindStateDoubleQuote:
			out.WriteByte(ch)
			if ch == '"' {
				if i+1 < len(query) && query[i+1] == '"' {
					out.WriteByte(query[i+1])
					i++
					continue
				}
				state = rebindStateDefault
			}
		case rebindStateLineComment:
			out.WriteByte(ch)
			if ch == '\n' {
				state = rebindStateDefault
			}
		case rebindStateBlockComment:
			out.WriteByte(ch)
			if ch == '*' && i+1 < len(query) && query[i+1] == '/' {
				out.WriteByte('/')
				i++
				state = rebindStateDefault
			}
		}
	}
	return out.String()
}

type rebindState int

const (
	rebindStateDefault rebindState = iota
	rebindStateSingleQuote
	rebindStateDoubleQuote
	rebindStateLineComment
	rebindStateBlockComment
)
