// Package executor defines the plugin contract every generation/media step
// implements, plus reflection helpers binding parameters to Go structs by
// json tag. Derived from github.com/BabySid/aether's executor package
// (BSD-3-Clause, Copyright (c) 2026, Master Sid; see ../LICENSE.aether).
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"aigc-platform/internal/infra/executor/spi/model"
)

// Plugin is a synchronous executor: Execute runs the whole step.
type Plugin interface {
	Type() string
	Schema() model.ExecutorSchema
	Execute(ctx context.Context, req *ExecuteRequest) (*model.ExecOutputs, error)
}

// ExecuteRequest carries one execution attempt of a node.
type ExecuteRequest struct {
	// TaskRunID is stable across attempts of the same node; assets record it
	// so they can be traced back to their job.
	TaskRunID     string
	WorkflowRunID string
	TaskName      string
	TemplateName  string

	Inputs     *model.Inputs
	Timeout    string
	RetryCount int
}

// Registry routes executor types to plugins.
type Registry struct {
	plugins map[string]Plugin
}

func NewRegistry() *Registry {
	return &Registry{plugins: make(map[string]Plugin)}
}

// Register adds a plugin; registering the same type twice is an error.
func (r *Registry) Register(plugin Plugin) error {
	t := plugin.Type()
	if _, exists := r.plugins[t]; exists {
		return fmt.Errorf("executor type %q already registered", t)
	}
	r.plugins[t] = plugin
	return nil
}

func (r *Registry) Get(executorType string) (Plugin, bool) {
	p, ok := r.plugins[executorType]
	return p, ok
}

func (r *Registry) Types() []string {
	types := make([]string, 0, len(r.plugins))
	for t := range r.plugins {
		types = append(types, t)
	}
	return types
}

// OutputFrom converts a flat output struct into ExecOutputs; each field's
// json tag becomes the parameter name.
func OutputFrom(output any) (*model.ExecOutputs, error) {
	t := reflect.TypeOf(output)
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("OutputFrom: expected struct, got %s", t.Kind())
	}
	v := reflect.ValueOf(output)
	var params []model.Parameter
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			return nil, fmt.Errorf("OutputFrom: embedded (anonymous) fields not supported: %s", f.Name)
		}
		jsonTag := f.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}
		name := parseJSONTagName(jsonTag)
		if name == "" {
			name = f.Name
		}
		raw, err := json.Marshal(v.Field(i).Interface())
		if err != nil {
			return nil, fmt.Errorf("OutputFrom: field %s: %w", name, err)
		}
		params = append(params, model.Parameter{Name: name, Value: raw})
	}
	return &model.ExecOutputs{Parameters: params}, nil
}

// BindInputs fills dst's fields from parameters whose names match the
// fields' json tags. Parameters without a matching field are ignored and
// fields without a parameter keep their zero value.
func BindInputs(inputs *model.Inputs, dst any) error {
	if inputs == nil {
		return nil
	}
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("BindInputs: dst must be a non-nil pointer, got %T", dst)
	}
	if rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("BindInputs: dst must point to a struct, got pointer to %s", rv.Elem().Kind())
	}
	index := make(map[string]json.RawMessage, len(inputs.Parameters))
	for _, p := range inputs.Parameters {
		index[p.Name] = p.Value
	}
	t := rv.Elem().Type()
	v := rv.Elem()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := parseJSONTagName(f.Tag.Get("json"))
		if name == "" || name == "-" {
			continue
		}
		raw, ok := index[name]
		if !ok || len(raw) == 0 {
			continue
		}
		if err := json.Unmarshal(raw, v.Field(i).Addr().Interface()); err != nil {
			return fmt.Errorf("BindInputs: field %s: %w", name, err)
		}
	}
	return nil
}

// DynamicOutputs marks an executor whose outputs are decided at runtime.
type DynamicOutputs struct{}

// SchemaOf derives a schema from a config struct C and output struct O.
func SchemaOf[C, O any](execType, version, description string) model.ExecutorSchema {
	schema := model.ExecutorSchema{Type: execType, Version: version, Description: description}
	if params := paramsOf[C](); len(params) > 0 {
		schema.Inputs = &model.Inputs{Parameters: params}
	}
	var zero O
	if t := reflect.TypeOf(zero); t != nil && t != reflect.TypeOf(DynamicOutputs{}) {
		if params := paramsOf[O](); len(params) > 0 {
			schema.Outputs = &model.ExecOutputs{Parameters: params}
		}
	}
	return schema
}

func paramsOf[T any]() []model.Parameter {
	var zero T
	t := reflect.TypeOf(zero)
	if t == nil {
		return nil
	}
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("SchemaOf: type parameter must be a struct, got %s", t.Kind()))
	}
	var out []model.Parameter
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		jsonTag := f.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}
		name := parseJSONTagName(jsonTag)
		if name == "" {
			name = f.Name
		}
		out = append(out, model.Parameter{Name: name, Type: paramType(f.Type), Description: f.Tag.Get("desc")})
	}
	return out
}

func parseJSONTagName(tag string) string {
	if idx := strings.Index(tag, ","); idx != -1 {
		return tag[:idx]
	}
	return tag
}

func paramType(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "int"
	case reflect.Float32, reflect.Float64:
		return "float"
	case reflect.Bool:
		return "bool"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	default:
		return "any"
	}
}
