package store

import (
	"database/sql/driver"
	"fmt"
	"strings"

	"modernc.org/sqlite"
)

// casefold(x) lower-cases text with Unicode rules. SQLite's own LIKE and
// lower() fold only ASCII, so a search for "анна" would miss "Анна".
func init() {
	if err := sqlite.RegisterDeterministicScalarFunction("casefold", 1, casefoldSQL); err != nil {
		panic(fmt.Sprintf("register casefold: %v", err))
	}
}

func casefoldSQL(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	switch v := args[0].(type) {
	case nil:
		return nil, nil
	case string:
		return strings.ToLower(v), nil
	case []byte:
		return strings.ToLower(string(v)), nil
	default:
		return strings.ToLower(fmt.Sprint(v)), nil
	}
}
