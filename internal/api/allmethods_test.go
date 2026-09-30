package api_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/api/apitest"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// callAll invokes every Local method of l with zero arguments and returns
// the error of each call by method name.
func callAll(t *testing.T, l api.Local) map[string]error {
	t.Helper()
	out := map[string]error{}
	v := reflect.ValueOf(l)
	it := reflect.TypeOf((*api.Local)(nil)).Elem()
	ctx := reflect.ValueOf(context.Background())
	for i := 0; i < it.NumMethod(); i++ {
		m := it.Method(i)
		fn := v.MethodByName(m.Name)
		args := []reflect.Value{ctx}
		for j := 1; j < m.Type.NumIn(); j++ {
			args = append(args, reflect.Zero(m.Type.In(j)))
		}
		res := fn.Call(args)
		errV := res[len(res)-1]
		if errV.IsNil() {
			out[m.Name] = nil
			continue
		}
		out[m.Name] = errV.Interface().(error)
	}
	return out
}

// TestEveryMethodCrossesTheWire calls every generated client method against
// an empty Stub: each must reach the server dispatcher and come back as
// DEY-X008 naming the method.
func TestEveryMethodCrossesTheWire(t *testing.T) {
	c := apitest.Serve(t, &apitest.Stub{})
	errs := callAll(t, c)
	require.Len(t, errs, reflect.TypeOf((*api.Local)(nil)).Elem().NumMethod())
	for name, err := range errs {
		e := requireCode(t, err, deyerr.X008)
		require.Contains(t, e.Message(), "api.Local."+name)
	}
}
