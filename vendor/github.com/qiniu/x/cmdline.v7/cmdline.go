package cmdline

import (
	"errors"
	"strings"

	. "qiniupkg.com/x/ctype.v7"
)

/* ---------------------------------------------------------------------------

Shell 基础规则：

* 多行字符串：用 '...' 或 "..."。其中 " 会自动转义 $(var)，而 ' 不会。
* 普通字符串：用 [ \t] 分隔。转义符以 \ 开头。
* 外部命令：`...`。

七牛规则：

* 外部命令： `...` 或 |...| 。
* 多行字符串：用 '...' 或 ```\n...``` 或 ===\n...=== 不转义。用 "..."，支持以 \ 开头的转义，也支持外部命令。
* 普通字符串：用 [ \t] 分隔。转义符以 \ 开头，同时也支持外部命令。
* 关于 $(var) 支持：每个命令自己执行 $(var) 的展开。不统一执行的原因是，在不同上下文需要不同的转义方式。

样例：

post http://rs.qiniu.com/delete/`base64 Bucket:Key`
auth `qbox AccessKey SecretKey`
ret  200

post http://rs.qiniu.com/batch
auth qboxtest
form op=/delete/`base64 Bucket:Key`&op=/delete/`base64 Bucket2:Key2`
ret  200

post http://rs.qiniu.com/batch
auth qboxtest
form op=/delete/`base64 Bucket:Key`&op=/delete/`base64 Bucket:NotExistKey`
ret  298
json '[
	{"code": 200}, {"code": 612}
]'
equal $(code1) 200

// -------------------------------------------------------------------------*/

var (
	EOF = errors.New("end of file")
	ErrUnsupportedFeatureSubCmd = errors.New("unsupported feature: sub command")
	ErrUnsupportedFeatureMultiCmds = errors.New("unsupported feature: multi commands")
	ErrInvalidEscapeChar = errors.New("invalid escape char")
	ErrIncompleteStringExpectQuot = errors.New("incomplete string, expect \"")
	ErrIncompleteStringExpectSquot = errors.New("incomplete string, expect '")
	ErrIncompleteStringExpectBacktick = errors.New("incomplete string, expect ` or |")
)

var (
	errEOL = errors.New("end of line")
)

// ---------------------------------------------------------------------------

func Skip(str string, typeMask uint32) string {

	for i := 0; i < len(str); i++ {
		if !Is(typeMask, rune(str[i])) {
			return str[i:]
		}
	}
	return ""
}

func Find(str string, typeMask uint32) (n int) {

	for n = 0; n < len(str); n++ {
		if Is(typeMask, rune(str[n])) {
			break
		}
	}
	return
}

// ---------------------------------------------------------------------------

// EOL = \r\n? | \n
//
func requireEOL(code string) (hasEOL bool, codeNext string) {

	if strings.HasPrefix(code, "\r") {
		if strings.HasPrefix(code[1:], "\n") {
			return true, code[2:]
		}
	} else if !strings.HasPrefix(code, "\n") {
		return false, code
	}
	return true, code[1:]
}

// ---------------------------------------------------------------------------

type Parser struct {
	ExecSub func(code string) (string, error)
	Escape  func(c byte) string
	comment bool
}

func NewParser() *Parser {

	return &Parser{
		ExecSub: defaultExecSub,
		Escape: defaultEscape,
	}
}

func defaultExecSub(code string) (string, error) {
	return "", ErrUnsupportedFeatureSubCmd
}

// ---------------------------------------------------------------------------

const (
	endOfLine    = EOL | SEMICOLON // [\r\n;]
	blanks       = SPACE_BAR | TAB
	blankAndEOLs = SPACE_BAR | TAB | endOfLine
)

const (
	endMask_QuotString = RDIV | BACKTICK | OR | QUOT			// [\\`|"]
	endMask_NonquotString = RDIV | BACKTICK | OR | blankAndEOLs	// [\\`| \t\r\n;]
)

func (p *Parser) parseString(
	code string, endMask uint32) (item string, ok bool, codeNext string, err error) {

	codeNext = code
	for {
		n := Find(codeNext, endMask)
		if n > 0 {
			item += codeNext[:n]
			ok = true
		}
		if len(codeNext) == n {
			codeNext = ""
			if endMask == endMask_QuotString {
				err = ErrIncompleteStringExpectQuot
			} else {
				err = EOF
			}
			return
		}
		switch codeNext[n] {
		case '\\':
			if len(codeNext) == n+1 {
				err = ErrInvalidEscapeChar
				return
			}
			item += p.Escape(codeNext[n+1])
			codeNext = codeNext[n+2:]
		case '`', '|':
			c := codeNext[n]
			codeNext = codeNext[n+1:]
			len := strings.IndexByte(codeNext, c)
			if len < 0 {
				err = ErrIncompleteStringExpectBacktick
				return
			}
			if !p.comment {
				valSub, errSub := p.ExecSub(codeNext[:len])
				if errSub != nil {
					err = errors.New("Exec `" + codeNext[:len] + "` failed: " + errSub.Error())
					return
				}
				item += valSub
			}
			codeNext = codeNext[len+1:]
		case '"':
			ok = true			
			codeNext = codeNext[n+1:]
			return
		default:
			if Is(endOfLine, rune(codeNext[n])) {
				err = errEOL
			}
			codeNext = codeNext[n+1:]
			return
		}
		ok = true			
	}
	return
}

func (p *Parser) parseItem(
	code string, skipMask uint32) (item string, ok bool, codeNext string, err error) {

	codeNext = Skip(code, skipMask)
	if len(codeNext) == 0 {
		err = EOF
		return
	}

	switch codeNext[0] {
	case '"':
		return p.parseString(codeNext[1:], endMask_QuotString)
	case '\'':
		codeNext = codeNext[1:]
		len := strings.IndexByte(codeNext, '\'')
		if len < 0 {
			err = ErrIncompleteStringExpectSquot
			return
		}
		return codeNext[:len], true, codeNext[len+1:], nil
	default:
		if strings.HasPrefix(codeNext, "```") || strings.HasPrefix(codeNext, "===") {
			endMark := codeNext[:3]
			_, codeNext = requireEOL(codeNext[3:])
			len := strings.Index(codeNext, endMark)
			if len < 0 {
				err = errors.New("incomplete string, expect " + endMark)
				return
			}
			return codeNext[:len], true, codeNext[len+3:], nil
		}
		return p.parseString(codeNext, endMask_NonquotString)
	}
}

func (p *Parser) ParseCmd(cmdline string) (cmd []string, err error) {

	cmd, _, err = p.ParseCode(cmdline)
	if err == EOF && len(cmd) > 0 {
		return cmd, nil
	}
	if err == nil {
		err = ErrUnsupportedFeatureMultiCmds
	}
	return
}

func (p *Parser) ParseCode(code string) (cmd []string, codeNext string, err error) {

	item, ok, codeNext, err := p.parseItem(code, blankAndEOLs)
	if !ok {
		return
	}
	p.comment = strings.HasPrefix(item, "#")

	cmd = append(cmd, item)
	for err == nil {
		item, ok, codeNext, err = p.parseItem(codeNext, blanks)
		if ok {
			cmd = append(cmd, item)
		}
	}
	if err == errEOL {
		err = nil
	}
	if p.comment {
		cmd = nil
	}
	return
}

// ---------------------------------------------------------------------------

