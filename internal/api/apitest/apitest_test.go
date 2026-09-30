package apitest

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// zeroCall calls method name of v with zero arguments after ctx.
func zeroCall(v reflect.Value, name string) error {
	fn := v.MethodByName(name)
	args := []reflect.Value{reflect.ValueOf(context.Background())}
	for j := 1; j < fn.Type().NumIn(); j++ {
		args = append(args, reflect.Zero(fn.Type().In(j)))
	}
	res := fn.Call(args)
	last := res[len(res)-1]
	if last.IsNil() {
		return nil
	}
	return last.Interface().(error)
}

func TestStubUnsetMethodsReturnX008(t *testing.T) {
	s := &Stub{}
	v := reflect.ValueOf(s)
	it := reflect.TypeOf((*api.Local)(nil)).Elem()
	for i := 0; i < it.NumMethod(); i++ {
		name := it.Method(i).Name
		err := zeroCall(v, name)
		require.Truef(t, deyerr.HasCode(err, deyerr.X008), "%s: %v", name, err)
	}
}

// TestStubSetMethodsAreCalled sets every XxxFn field to a function returning
// nil results and checks each method delegates to it.
func TestStubSetMethodsAreCalled(t *testing.T) {
	s := &Stub{}
	sv := reflect.ValueOf(s).Elem()
	calls := map[string]int{}
	for i := 0; i < sv.NumField(); i++ {
		f := sv.Field(i)
		name := sv.Type().Field(i).Name
		fn := reflect.MakeFunc(f.Type(), func([]reflect.Value) []reflect.Value {
			calls[name]++
			out := make([]reflect.Value, f.Type().NumOut())
			for j := range out {
				out[j] = reflect.Zero(f.Type().Out(j))
			}
			return out
		})
		f.Set(fn)
	}
	v := reflect.ValueOf(s)
	it := reflect.TypeOf((*api.Local)(nil)).Elem()
	for i := 0; i < it.NumMethod(); i++ {
		name := it.Method(i).Name
		require.NoError(t, zeroCall(v, name), name)
		require.Equal(t, 1, calls[name+"Fn"], name)
	}
}

func TestServe(t *testing.T) {
	c := Serve(t, &Stub{StatusFn: func(context.Context) (api.Status, error) {
		return api.Status{Role: "hub"}, nil
	}})
	st, err := c.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, "hub", st.Role)
	_, err = c.NodeList(context.Background())
	require.True(t, deyerr.HasCode(err, deyerr.X008))
}
