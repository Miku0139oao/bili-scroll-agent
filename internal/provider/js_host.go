package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// installHostAPI injects the only host surface an adapter can reach.
//
// Everything is passed as arguments to fetchBalance (via the __bili_ctx global
// read once by the caller) instead of being installed as a permanent global.
func installHostAPI(vm *goja.Runtime, opts Options, cfg Config, env *FetchEnv) error {
	log := func(call goja.FunctionCall) goja.Value {
		message := ""
		if len(call.Arguments) > 0 && !goja.IsUndefined(call.Arguments[0]) {
			message = call.Arguments[0].String()
		}
		env.Log(message)
		return goja.Undefined()
	}
	request := func(call goja.FunctionCall) goja.Value {
		response, err := hostRequest(vm, env, call)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return vm.ToValue(response)
	}
	httpObject := vm.NewObject()
	_ = httpObject.Set("request", request)
	_ = httpObject.Set("get", func(call goja.FunctionCall) goja.Value {
		options := map[string]any{"method": http.MethodGet}
		if len(call.Arguments) > 0 {
			options["url"] = call.Arguments[0].Export()
		}
		if len(call.Arguments) > 1 {
			mergeOptions(options, call.Arguments[1].Export())
		}
		return requestOptions(vm, env, options)
	})
	_ = httpObject.Set("post", func(call goja.FunctionCall) goja.Value {
		options := map[string]any{"method": http.MethodPost}
		if len(call.Arguments) > 0 {
			options["url"] = call.Arguments[0].Export()
		}
		if len(call.Arguments) > 1 {
			switch body := call.Arguments[1].Export().(type) {
			case string:
				options["body"] = body
			default:
				options["json"] = body
			}
		}
		if len(call.Arguments) > 2 {
			mergeOptions(options, call.Arguments[2].Export())
		}
		return requestOptions(vm, env, options)
	})

	ctxObject := vm.NewObject()
	_ = ctxObject.Set("providerId", cfg.ProviderID)
	_ = ctxObject.Set("baseUrl", cfg.BaseURL)
	_ = ctxObject.Set("apiKey", cfg.APIKey)
	_ = ctxObject.Set("currency", cfg.Currency)
	_ = ctxObject.Set("settings", cfg.Settings)
	_ = ctxObject.Set("http", httpObject)
	_ = ctxObject.Set("log", log)
	_ = ctxObject.Set("now", func(goja.FunctionCall) goja.Value {
		return vm.ToValue(time.Now().UTC().Format(time.RFC3339))
	})
	return vm.Set("__bili_ctx", ctxObject)
}

func hostRequest(vm *goja.Runtime, env *FetchEnv, call goja.FunctionCall) (HTTPResponse, error) {
	if len(call.Arguments) == 0 {
		return HTTPResponse{}, fmt.Errorf("http.request requires an options object")
	}
	options, ok := call.Arguments[0].Export().(map[string]any)
	if !ok {
		return HTTPResponse{}, fmt.Errorf("http.request requires an options object")
	}
	return requestOptionsGo(env, options)
}

func requestOptions(vm *goja.Runtime, env *FetchEnv, options map[string]any) goja.Value {
	response, err := requestOptionsGo(env, options)
	if err != nil {
		panic(vm.NewGoError(err))
	}
	return asJSResponse(vm, response)
}

func asJSResponse(vm *goja.Runtime, response HTTPResponse) goja.Value {
	// Expose the response as a plain object so adapters see lower-case keys
	// (json/body/status/headers) rather than Go field names.
	return vm.ToValue(map[string]any{
		"status":  response.Status,
		"headers": response.Headers,
		"body":    response.Body,
		"json":    response.JSON,
	})
}

func requestOptionsGo(env *FetchEnv, options map[string]any) (HTTPResponse, error) {
	request := fetchOptions{
		Method:  strings.TrimSpace(stringOf(options["method"])),
		URL:     strings.TrimSpace(stringOf(options["url"])),
		Headers: map[string]string{},
	}
	if value, ok := options["throw"].(bool); ok {
		request.Throw = value
	}
	if headers, ok := options["headers"].(map[string]any); ok {
		for key, raw := range headers {
			request.Headers[key] = fmt.Sprint(raw)
		}
	}
	switch body := options["body"].(type) {
	case string:
		request.Body = []byte(body)
	case map[string]any, []any:
		encoded, err := json.Marshal(body)
		if err != nil {
			return HTTPResponse{}, err
		}
		request.Body = encoded
		request.Headers["Content-Type"] = "application/json"
	}
	if body := options["json"]; body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return HTTPResponse{}, err
		}
		request.Body = encoded
		request.Headers["Content-Type"] = "application/json"
	}
	if request.URL == "" {
		return HTTPResponse{}, fmt.Errorf("http request is missing a URL")
	}
	return env.HTTP(env.ctx, request)
}

func mergeOptions(target map[string]any, source any) {
	extra, ok := source.(map[string]any)
	if !ok {
		return
	}
	for key, value := range extra {
		if value != nil {
			target[key] = value
		}
	}
}

func stringOf(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}
