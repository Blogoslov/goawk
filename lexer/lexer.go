// Package lexer is an AWK lexer (tokenizer).
//
// The lexer turns a string of AWK source code into a stream of
// tokens for parsing.
//
// To tokenize some source, create a new lexer with NewLexer(src) and
// then call Scan() until the token type is EOF or ILLEGAL.
//
package lexer

import (
	"fmt"
	"unicode/utf8"
)

// Lexer tokenizes a byte string of AWK source code. Use NewLexer to
// actually create a lexer, and Scan() or ScanRegex() to get tokens.
type Lexer struct {
	src      []byte
	offset   int
	ch       rune
	errorMsg string
	pos      Position
	nextPos  Position
	hadSpace bool
	lastTok  Token
}

// Position stores the source line and column where a token starts.
type Position struct {
	Line   int
	Column int
}

// NewLexer creates a new lexer that will tokenize the given source
// code. See the module-level example for a working example.
func NewLexer(src []byte) *Lexer {
	l := &Lexer{src: src}
	l.nextPos.Line = 1
	l.nextPos.Column = 1
	l.next()
	return l
}

// HadSpace returns true if the previously-scanned token had
// whitespace before it. Used by the parser because when calling a
// user-defined function the grammar doesn't allow a space between
// the function name and the left parenthesis.
func (l *Lexer) HadSpace() bool {
	return l.hadSpace
}

// Scan scans the next token and returns its position (line/column),
// token value (one of the uppercased token constants), and the
// string value of the token. For most tokens, the token value is
// empty. For NAME, NUMBER, STRING, and REGEX tokens, it's the
// token's value. For an ILLEGAL token, it's the error message.
func (l *Lexer) Scan() (Position, Token, string) {
	pos, tok, val := l.scan()
	l.lastTok = tok
	return pos, tok, val
}

func (l *Lexer) scan() (Position, Token, string) {
	l.hadSpace = false
	for l.ch == ' ' || l.ch == '\t' || l.ch == '\r' || l.ch == '\\' {
		l.hadSpace = true
		if l.ch == '\\' {
			l.next()
			if l.ch == '\r' {
				l.next()
			}
			if l.ch != '\n' {
				return l.pos, ILLEGAL, "expected \\n after \\ line continuation"
			}
		}
		l.next()
	}
	if l.ch == '#' {
		// Skip comment till end of line
		l.next()
		for l.ch != '\n' && l.ch >= 0 {
			l.next()
		}
	}
	if l.ch < 0 {
		if l.errorMsg != "" {
			return l.pos, ILLEGAL, l.errorMsg
		}
		return l.pos, EOF, ""
	}

	pos := l.pos
	tok := ILLEGAL
	val := ""

	ch := l.ch
	l.next()

	// Names: keywords and functions
	if isNameStart(ch) {
		runes := []rune{ch}
		for isNameStart(l.ch) || (l.ch >= '0' && l.ch <= '9') {
			runes = append(runes, l.ch)
			l.next()
		}
		name := string(runes)
		tok, isKeyword := keywordTokens[name]
		if !isKeyword {
			tok = NAME
			val = name
		}
		return pos, tok, val
	}

	switch ch {
	case '\n':
		tok = NEWLINE
	case ':':
		tok = COLON
	case ',':
		tok = COMMA
	case '/':
		tok = l.choice('=', DIV, DIV_ASSIGN)
	case '{':
		tok = LBRACE
	case '[':
		tok = LBRACKET
	case '(':
		tok = LPAREN
	case '-':
		switch l.ch {
		case '-':
			l.next()
			tok = DECR
		case '=':
			l.next()
			tok = SUB_ASSIGN
		default:
			tok = SUB
		}
	case '%':
		tok = l.choice('=', MOD, MOD_ASSIGN)
	case '+':
		switch l.ch {
		case '+':
			l.next()
			tok = INCR
		case '=':
			l.next()
			tok = ADD_ASSIGN
		default:
			tok = ADD
		}
	case '}':
		tok = RBRACE
	case ']':
		tok = RBRACKET
	case ')':
		tok = RPAREN
	case '*':
		switch l.ch {
		case '*':
			l.next()
			tok = l.choice('=', POW, POW_ASSIGN)
		case '=':
			l.next()
			tok = MUL_ASSIGN
		default:
			tok = MUL
		}
	case '=':
		tok = l.choice('=', ASSIGN, EQUALS)
	case '^':
		tok = l.choice('=', POW, POW_ASSIGN)
	case '!':
		switch l.ch {
		case '=':
			l.next()
			tok = NOT_EQUALS
		case '~':
			l.next()
			tok = NOT_MATCH
		default:
			tok = NOT
		}
	case '<':
		tok = l.choice('=', LESS, LTE)
	case '>':
		switch l.ch {
		case '=':
			l.next()
			tok = GTE
		case '>':
			l.next()
			tok = APPEND
		default:
			tok = GREATER
		}
	case '~':
		tok = MATCH
	case '?':
		tok = QUESTION
	case ';':
		tok = SEMICOLON
	case '$':
		tok = DOLLAR
	case '&':
		tok = l.choice('&', ILLEGAL, AND)
		if tok == ILLEGAL {
			return l.pos, ILLEGAL, fmt.Sprintf("unexpected %q after '&'", l.ch)
		}
	case '|':
		tok = l.choice('|', PIPE, OR)
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '.':
		runes := []rune{ch}
		gotDigit := false
		if ch != '.' {
			gotDigit = true
			for l.ch >= '0' && l.ch <= '9' {
				runes = append(runes, l.ch)
				l.next()
			}
			if l.ch == '.' {
				runes = append(runes, l.ch)
				l.next()
			}
		}
		for l.ch >= '0' && l.ch <= '9' {
			gotDigit = true
			runes = append(runes, l.ch)
			l.next()
		}
		if !gotDigit {
			return l.pos, ILLEGAL, "expected digits"
		}
		if l.ch == 'e' || l.ch == 'E' {
			runes = append(runes, l.ch)
			l.next()
			if l.ch == '+' || l.ch == '-' {
				runes = append(runes, l.ch)
				l.next()
			}
			for l.ch >= '0' && l.ch <= '9' {
				runes = append(runes, l.ch)
				l.next()
			}
		}
		tok = NUMBER
		val = string(runes)
	case '"', '\'':
		// Note: POSIX awk spec doesn't allow single-quoted strings,
		// but this helps without quoting, especially on Windows
		// where the shell quote character is " (double quote).
		runes := []rune{}
		for l.ch != ch {
			c := l.ch
			if c < 0 {
				return l.pos, ILLEGAL, "didn't find end quote in string"
			}
			if c == '\r' || c == '\n' {
				return l.pos, ILLEGAL, "can't have newline in string"
			}
			if c == '\\' {
				l.next()
				switch l.ch {
				case 't':
					c = '\t'
				case 'r':
					c = '\r'
				case 'n':
					c = '\n'
				default:
					c = l.ch
				}
			}
			runes = append(runes, c)
			l.next()
		}
		l.next()
		tok = STRING
		val = string(runes)
	default:
		tok = ILLEGAL
		val = fmt.Sprintf("unexpected %q", ch)
	}
	return pos, tok, val
}

// ScanRegex parses an AWK regular expression in /slash/ syntax. The
// AWK grammar has somewhat special handling of regex tokens, so the
// parser can only call this after a DIV or DIV_ASSIGN token has just
// been scanned.
func (l *Lexer) ScanRegex() (Position, Token, string) {
	pos, tok, val := l.scanRegex()
	l.lastTok = tok
	return pos, tok, val
}

func (l *Lexer) scanRegex() (Position, Token, string) {
	pos := l.pos
	runes := []rune{}
	switch l.lastTok {
	case DIV:
		// Regex after '/' (the usual case)
		pos.Column -= 1
	case DIV_ASSIGN:
		// Regex after '/=' (possible when regex starts with '=')
		pos.Column -= 2
		runes = append(runes, '=')
	default:
		return l.pos, ILLEGAL, fmt.Sprintf("unexpected %s preceding regex", l.lastTok)
	}
	for l.ch != '/' {
		c := l.ch
		if c < 0 {
			return l.pos, ILLEGAL, "didn't find end slash in regex"
		}
		if c == '\r' || c == '\n' {
			return l.pos, ILLEGAL, "can't have newline in regex"
		}
		if c == '\\' {
			l.next()
			if l.ch != '/' {
				runes = append(runes, '\\')
			}
			c = l.ch
		}
		runes = append(runes, c)
		l.next()
	}
	l.next()
	return pos, REGEX, string(runes)
}

func (l *Lexer) next() {
	l.pos = l.nextPos
	ch, size := utf8.DecodeRune(l.src[l.offset:])
	if size == 0 {
		l.ch = -1
		return
	}
	if ch == utf8.RuneError {
		l.ch = -1
		l.errorMsg = fmt.Sprintf("invalid UTF-8 byte 0x%02x", l.src[l.offset])
		return
	}
	if ch == '\n' {
		l.nextPos.Line++
		l.nextPos.Column = 1
	} else {
		l.nextPos.Column++
	}
	l.ch = ch
	l.offset += size
}

func isNameStart(ch rune) bool {
	return ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func (l *Lexer) choice(ch rune, one, two Token) Token {
	if l.ch == ch {
		l.next()
		return two
	}
	return one
}
