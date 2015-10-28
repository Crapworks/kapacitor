package tick

import (
	"errors"
	"fmt"
	"math"
	"regexp"
)

var ErrInvalidExpr = errors.New("expression is invalid, could not evaluate")

// Expression functions are stateful. Their state is updated with
// each call to the function. A StatefulExpr is a Node
// and its associated function state.
type StatefulExpr struct {
	Node  Node
	Funcs Funcs
}

func NewStatefulExpr(n Node) *StatefulExpr {
	return &StatefulExpr{
		Node:  n,
		Funcs: NewFunctions(),
	}
}

// Reset the state
func (s *StatefulExpr) Reset() {
	for _, f := range s.Funcs {
		f.Reset()
	}
}

// Lookup for variables
type Vars map[string]interface{}

func (s *StatefulExpr) EvalBool(v Vars) (bool, error) {
	stck := &stack{}
	err := s.eval(s.Node, v, stck)
	if err != nil {
		return false, err
	}
	if stck.Len() == 1 {
		value := stck.Pop()
		// Resolve reference
		if ref, ok := value.(*ReferenceNode); ok {
			value = v[ref.Reference]
		}
		b, ok := value.(bool)
		if ok {
			return b, nil
		} else {
			return false, fmt.Errorf("expression returned unexpected type %T", value)
		}
	}
	return false, ErrInvalidExpr
}

func (s *StatefulExpr) EvalNum(v Vars) (float64, error) {
	stck := &stack{}
	err := s.eval(s.Node, v, stck)
	if err != nil {
		return math.NaN(), err
	}
	if stck.Len() == 1 {
		value := stck.Pop()
		// Resolve reference
		if ref, ok := value.(*ReferenceNode); ok {
			value = v[ref.Reference]
		}
		n, ok := value.(float64)
		if ok {
			return n, nil
		} else {
			return math.NaN(), fmt.Errorf("expression returned unexpected type %T", value)
		}
	}
	return math.NaN(), ErrInvalidExpr
}

func (s *StatefulExpr) eval(n Node, v Vars, stck *stack) (err error) {
	switch node := n.(type) {
	case *BoolNode:
		stck.Push(node.Bool)
	case *NumberNode:
		if node.IsInt {
			stck.Push(node.Int64)
		} else {
			stck.Push(node.Float64)
		}
	case *DurationNode:
		stck.Push(node.Dur)
	case *StringNode:
		stck.Push(node.Literal)
	case *RegexNode:
		stck.Push(node.Regex)
	case *UnaryNode:
		err = s.eval(node.Node, v, stck)
		if err != nil {
			return
		}
		s.evalUnary(node.Operator, v, stck)
	case *BinaryNode:
		err = s.eval(node.Left, v, stck)
		if err != nil {
			return
		}
		err = s.eval(node.Right, v, stck)
		if err != nil {
			return
		}
		err = s.evalBinary(node.Operator, v, stck)
		if err != nil {
			return
		}
	case *FunctionNode:
		args := make([]interface{}, len(node.Args))
		for i, arg := range node.Args {
			err = s.eval(arg, v, stck)
			if err != nil {
				return
			}
			a := stck.Pop()
			if r, ok := a.(*ReferenceNode); ok {
				a = v[r.Reference]
				if a == nil {
					return fmt.Errorf("undefined variable %s", r.Reference)
				}
			}
			args[i] = a
		}
		// Call function
		f := s.Funcs[node.Func]
		if f == nil {
			return fmt.Errorf("undefined function %s", node.Func)
		}
		ret, err := f.Call(args...)
		if err != nil {
			return fmt.Errorf("error calling %s: %s", node.Func, err)
		}
		stck.Push(ret)
	default:
		stck.Push(node)
	}
	return nil
}

func (s *StatefulExpr) evalUnary(op tokenType, vars Vars, stck *stack) error {
	v := stck.Pop()
	switch op {
	case tokenMinus:
		switch n := v.(type) {
		case float64:
			stck.Push(-1 * n)
		case int64:
			stck.Push(-1 * n)
		default:
			return fmt.Errorf("invalid arugument to '-' %v", v)
		}
	case tokenNot:
		if b, ok := v.(bool); ok {
			stck.Push(!b)
		} else {
			return fmt.Errorf("invalid arugument to '!' %v", v)
		}
	}
	return nil
}

var ErrMismatchedTypes = errors.New("operands of binary operators must be of the same type, use bool(), int() and float() as needed")

func (s *StatefulExpr) evalBinary(op tokenType, vars Vars, stck *stack) (err error) {
	r := stck.Pop()
	l := stck.Pop()
	// Resolve any references
	if ref, ok := l.(*ReferenceNode); ok {
		l = vars[ref.Reference]
	}
	if ref, ok := r.(*ReferenceNode); ok {
		r = vars[ref.Reference]
	}
	var v interface{}
	switch {
	case isMathOperator(op):
		switch ln := l.(type) {
		case int64:
			rn, ok := r.(int64)
			if !ok {
				return ErrMismatchedTypes
			}
			v, err = doIntMath(op, ln, rn)
		case float64:
			rn, ok := r.(float64)
			if !ok {
				return ErrMismatchedTypes
			}
			v, err = doFloatMath(op, ln, rn)
		default:
			return ErrMismatchedTypes
		}
	case isCompOperator(op):
		switch ln := l.(type) {
		case bool:
			rn, ok := r.(bool)
			if !ok {
				return ErrMismatchedTypes
			}
			v, err = doBoolComp(op, ln, rn)
		case int64:
			lf := float64(ln)
			var rf float64
			switch rn := r.(type) {
			case int64:
				rf = float64(rn)
			case float64:
				rf = rn
			default:
				return ErrMismatchedTypes
			}
			v, err = doFloatComp(op, lf, rf)
		case float64:
			var rf float64
			switch rn := r.(type) {
			case int64:
				rf = float64(rn)
			case float64:
				rf = rn
			default:
				return ErrMismatchedTypes
			}
			v, err = doFloatComp(op, ln, rf)
		case string:
			rn, ok := r.(string)
			if ok {
				v, err = doStringComp(op, ln, rn)
			} else if rx, ok := r.(*regexp.Regexp); ok {
				v, err = doRegexComp(op, ln, rx)
			} else {
				return ErrMismatchedTypes
			}
		default:
			return ErrMismatchedTypes
		}
	default:
		return fmt.Errorf("return: unknown operator %v", op)
	}
	if err != nil {
		return
	}
	stck.Push(v)
	return
}

func doIntMath(op tokenType, l, r int64) (v int64, err error) {
	switch op {
	case tokenPlus:
		v = l + r
	case tokenMinus:
		v = l - r
	case tokenMult:
		v = l * r
	case tokenDiv:
		v = l / r
	default:
		return 0, fmt.Errorf("invalid integer math operator %v", op)
	}
	return
}

func doFloatMath(op tokenType, l, r float64) (v float64, err error) {
	switch op {
	case tokenPlus:
		v = l + r
	case tokenMinus:
		v = l - r
	case tokenMult:
		v = l * r
	case tokenDiv:
		v = l / r
	default:
		return math.NaN(), fmt.Errorf("invalid float math operator %v", op)
	}
	return
}

func doBoolComp(op tokenType, l, r bool) (v bool, err error) {
	switch op {
	case tokenEqual:
		v = l == r
	case tokenNotEqual:
		v = l != r
	case tokenAnd:
		v = l && r
	case tokenOr:
		v = l || r
	default:
		err = fmt.Errorf("invalid boolean comparison operator %v", op)
	}
	return
}

func doFloatComp(op tokenType, l, r float64) (v bool, err error) {
	switch op {
	case tokenEqual:
		v = l == r
	case tokenNotEqual:
		v = l != r
	case tokenLess:
		v = l < r
	case tokenGreater:
		v = l > r
	case tokenLessEqual:
		v = l <= r
	case tokenGreaterEqual:
		v = l >= r
	default:
		err = fmt.Errorf("invalid float comparison operator %v", op)
	}
	return
}

func doStringComp(op tokenType, l, r string) (v bool, err error) {
	switch op {
	case tokenEqual:
		v = l == r
	case tokenNotEqual:
		v = l != r
	case tokenLess:
		v = l < r
	case tokenGreater:
		v = l > r
	case tokenLessEqual:
		v = l <= r
	case tokenGreaterEqual:
		v = l >= r
	default:
		err = fmt.Errorf("invalid string comparison operator %v", op)
	}
	return
}

func doRegexComp(op tokenType, l string, r *regexp.Regexp) (v bool, err error) {
	switch op {
	case tokenRegexEqual:
		v = r.MatchString(l)
	case tokenRegexNotEqual:
		v = !r.MatchString(l)
	default:
		err = fmt.Errorf("invalid regex comparison operator %v", op)
	}
	return
}