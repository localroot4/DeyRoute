package api_test

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/api/apitest"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// TestUnimplementedLocalAnswersWrongRole checks that every method of the
// generated UnimplementedLocal returns DEY-X009 naming both roles, directly
// and across the wire.
func TestUnimplementedLocalAnswersWrongRole(t *testing.T) {
	u := api.UnimplementedLocal{Role: "node", Need: "hub"}
	for _, l := range []api.Local{u, apitest.Serve(t, u)} {
		errs := callAll(t, l)
		require.Len(t, errs, reflect.TypeOf((*api.Local)(nil)).Elem().NumMethod())
		for name, err := range errs {
			e := requireCode(t, err, deyerr.X009)
			require.Equalf(t, "This command needs a hub, but this server is a node", e.Message(), "method %s", name)
		}
	}
}
