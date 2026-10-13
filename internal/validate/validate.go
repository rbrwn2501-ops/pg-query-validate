package validate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/serhii-chechun/pg-query-validate/internal/diagnostic"
	"github.com/serhii-chechun/pg-query-validate/internal/meta"

	"google.golang.org/protobuf/reflect/protoreflect"

	pgq "github.com/pganalyze/pg_query_go/v6"

	"github.com/pganalyze/pg_query_go/v6/parser"
)

type Error struct{ count int }

func (e *Error) Error() string {
	if e.count == 1 {
		return "1 problem found"
	}
	return fmt.Sprintf("%d problems found", e.count)
}

func Process(name string, r io.Reader) error {
	src, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("reading input: %w", err)
	}

	masked := meta.Mask(src)
	tree, err := pgq.Parse(string(masked))
	if err != nil {
		if d, ok := parseErrorDiagnostic(err, masked); ok {
			diagnostic.Write(os.Stderr, name, src, []diagnostic.Results{d})
			return &Error{count: 1}
		}
		return fmt.Errorf("PG_SQL parsing: %w", err)
	}

	diags := validate(tree, src)
	if len(diags) == 0 {
		return nil
	}
	diagnostic.Write(os.Stderr, name, src, diags)
	return &Error{count: len(diags)}
}

// nodeFullName is the protobuf full name of pg_query.Node, the wrapper message
// whose oneof selects the concrete AST node type.
const nodeFullName = protoreflect.FullName("pg_query.Node")

// typeNameFullName is the protobuf full name of pg_query.TypeName, the node
// describing a type reference such as a column type or a cast target.
const typeNameFullName = protoreflect.FullName("pg_query.TypeName")

// validate walks the AST and reports every structurally malformed node, along
// with any type name that looks misspelled. src is the source the tree was
// parsed from, used to map findings back to byte offsets.
func validate(tree *pgq.ParseResult, src []byte) []diagnostic.Results {
	if tree == nil {
		return []diagnostic.Results{{Message: "parse result is nil", Offset: -1}}
	}

	var diags []diagnostic.Results
	for i, raw := range tree.Stmts {
		path := fmt.Sprintf("statement %d", i+1)
		switch {
		case raw == nil:
			diags = append(diags, diagnostic.Results{Message: path + ": missing statement", Offset: -1})
			continue
		case raw.Stmt == nil:
			diags = append(diags, diagnostic.Results{Message: path + ": missing statement node", Offset: -1})
			continue
		}
		validateMessage(raw.ProtoReflect(), src, path, &diags)
	}
	return diags
}

// validateMessage recursively visits every message reachable from m and appends
// a finding for each Node whose type is unset.
func validateMessage(m protoreflect.Message, src []byte, path string, diags *[]diagnostic.Results) {
	if m.Descriptor().FullName() == typeNameFullName {
		if d, ok := validateTypeName(m, src); ok {
			*diags = append(*diags, d)
		}
	}

	if m.Descriptor().FullName() == nodeFullName {
		od := m.Descriptor().Oneofs().ByName("node")
		if od == nil || m.WhichOneof(od) == nil {
			*diags = append(*diags, diagnostic.Results{
				Message: fmt.Sprintf("%s: empty node (no node type set)", path),
				Offset:  -1,
			})
			return
		}
	}

	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind && fd.Kind() != protoreflect.GroupKind {
			return true
		}
		switch {
		case fd.IsList():
			list := v.List()
			for i := 0; i < list.Len(); i++ {
				validateMessage(list.Get(i).Message(), src, fmt.Sprintf("%s.%s[%d]", path, fd.Name(), i), diags)
			}
		case fd.IsMap():
			// pg_query's schema has no map fields; nothing to walk.
		default:
			validateMessage(v.Message(), src, fmt.Sprintf("%s.%s", path, fd.Name()), diags)
		}
		return true
	})
}

// validateTypeName reports an unqualified type name that looks like a
// misspelling of a built-in type. The parser accepts any identifier as a type
// name and only resolves it during semantic analysis, so an unknown type such
// as VARCHAT is invisible to a syntax check unless we look for it here.
func validateTypeName(m protoreflect.Message, src []byte) (diagnostic.Results, bool) {
	tn, ok := m.Interface().(*pgq.TypeName)
	if !ok {
		return diagnostic.Results{}, false
	}

	// A schema-qualified name (pg_catalog.varchar, myschema.status) is resolved
	// elsewhere; only a bare identifier can be a built-in typo.
	names := tn.GetNames()
	if len(names) != 1 {
		return diagnostic.Results{}, false
	}

	name := names[0].GetString_().GetSval()
	if name == "" || isBuiltinType(name) {
		return diagnostic.Results{}, false
	}
	suggestion, ok := suggestTypeName(name)
	if !ok {
		return diagnostic.Results{}, false
	}

	offset, length := diagnostic.IdentifierSpan(src, int(tn.GetLocation()), name)
	return diagnostic.Results{
		Message: fmt.Sprintf("unknown type %q (did you mean %q?)", name, suggestion),
		Offset:  offset,
		Length:  length,
	}, true
}

// parseErrorDiagnostic converts a PostgreSQL parser error into a source-anchored
// finding.
func parseErrorDiagnostic(err error, masked []byte) (diagnostic.Results, bool) {
	var pe *parser.Error
	if !errors.As(err, &pe) {
		return diagnostic.Results{}, false
	}

	offset := cursorByteOffset(masked, pe.Cursorpos)
	length := 1
	if tok, ok := errorToken(pe.Message); ok &&
		offset >= 0 && offset+len(tok) <= len(masked) &&
		strings.EqualFold(string(masked[offset:offset+len(tok)]), tok) {
		length = len(tok)
	}

	if len(masked) > 0 && offset >= len(masked) {
		end := len(masked)
		for end > 0 && (masked[end-1] == '\n' || masked[end-1] == '\r') {
			end--
		}
		if end > 0 {
			offset = end - 1
		} else {
			offset = len(masked) - 1
		}
		length = 1
	}
	return diagnostic.Results{Message: pe.Message, Offset: offset, Length: length}, true
}

// cursorByteOffset converts a 1-based character position.
func cursorByteOffset(src []byte, cursorpos int) int {
	if cursorpos < 1 {
		return -1
	}
	chars := cursorpos - 1
	for i := 0; i < len(src); {
		if chars == 0 {
			return i
		}
		_, size := utf8.DecodeRune(src[i:])
		i += size
		chars--
	}
	return len(src)
}

// errorToken extracts the token quoted by the "at or near \"...\"" clause of a
// parser error message, or reports false when the message has no such clause.
func errorToken(message string) (string, bool) {
	const marker = "at or near "
	i := strings.Index(message, marker)
	if i < 0 {
		return "", false
	}
	rest := message[i+len(marker):]
	if len(rest) < 2 || rest[0] != '"' {
		return "", false
	}
	j := strings.IndexByte(rest[1:], '"')
	if j < 0 {
		return "", false
	}
	return rest[1 : 1+j], true
}
