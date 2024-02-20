package jsonschema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/davron112/lura/v2/config"
	"github.com/davron112/lura/v2/logging"
	"github.com/davron112/lura/v2/proxy"
	"github.com/xeipuuv/gojsonschema"
)

const Namespace = "github.com/davron112/krakend-jsonschema"

var ErrEmptyBody = &malformedError{err: errors.New("could not validate an empty body")}

// ProxyFactory creates an proxy factory over the injected one adding a JSON Schema
// validator middleware to the pipe when required
func ProxyFactory(logger logging.Logger, pf proxy.Factory) proxy.FactoryFunc {
	return proxy.FactoryFunc(func(cfg *config.EndpointConfig) (proxy.Proxy, error) {
		next, err := pf.New(cfg)
		if err != nil {
			return proxy.NoopProxy, err
		}
		schemaLoader, ok := configGetter(cfg.ExtraConfig).(gojsonschema.JSONLoader)
		if !ok || schemaLoader == nil {
			return next, nil
		}
		schema, err := gojsonschema.NewSchema(schemaLoader)
		if err != nil {
			logger.Error("[ENDPOINT: " + cfg.Endpoint + "][JSONSchema] Parsing the definition:" + err.Error())
			return next, nil
		}
		logger.Debug("[ENDPOINT: " + cfg.Endpoint + "][JSONSchema] Validator enabled")
		return newProxy(schema, next), nil
	})
}

func newProxy(schema *gojsonschema.Schema, next proxy.Proxy) proxy.Proxy {
	return func(ctx context.Context, r *proxy.Request) (*proxy.Response, error) {
		if r.Body == nil {
			return nil, ErrEmptyBody
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		r.Body.Close()
		if len(body) == 0 {
			return nil, ErrEmptyBody
		}
		r.Body = io.NopCloser(bytes.NewBuffer(body))

		result, err := schema.Validate(gojsonschema.NewBytesLoader(body))
		if err != nil {
			return nil, &malformedError{err: err}
		}
		if !result.Valid() {
			return nil, &validationError{errs: result.Errors()}
		}

		return next(ctx, r)
	}
}

func configGetter(cfg config.ExtraConfig) interface{} {
	v, ok := cfg[Namespace]
	if !ok {
		return nil
	}
	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(v); err != nil {
		return nil
	}
	return gojsonschema.NewBytesLoader(buf.Bytes())
}

type validationError struct {
	errs []gojsonschema.ResultError
}

func (v *validationError) Error() string {
	errs := make([]string, len(v.errs))
	for i, desc := range v.errs {
		errs[i] = fmt.Sprintf("- %s", desc)
	}
	return strings.Join(errs, "\n")
}

func (*validationError) StatusCode() int {
	return http.StatusBadRequest
}

type malformedError struct {
	err error
}

func (m *malformedError) Error() string {
	return m.err.Error()
}

func (*malformedError) StatusCode() int {
	return http.StatusBadRequest
}
