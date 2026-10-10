package validator

import v10 "github.com/go-playground/validator/v10"

type Validator struct{ engine *v10.Validate }

func New() *Validator                       { return &Validator{engine: v10.New()} }
func (v *Validator) Struct(value any) error { return v.engine.Struct(value) }
