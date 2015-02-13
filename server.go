// Package searpc implements RPC framework for Seafile.
// It doesn't include transports, only provides function call and encode/decode.
package searpc

import (
	"errors"
	"encoding/json"
	"log"
	"reflect"
	"sync"
	"unicode"
	"unicode/utf8"
)

// service is a set of functions.
type service struct {
	name   string                     // name of service
	rcvr   reflect.Value              // receiver of methods for the service
	typ    reflect.Type               // type of the receiver
	method map[string]*reflect.Method // registered methods
}

// Server represents an RPC Server.
type Server struct {
	lock       sync.RWMutex // protects the serviceMap
	serviceMap map[string]*service
}

type Result struct {
	Ret     interface{} `json:"ret"`
	ErrCode int         `json:"err_code"`
	ErrMsg  string      `json:"err_msg"`
}

var typeOfResult = reflect.TypeOf((*Result)(nil)).Elem()

// NewServer returns a new Server.
func NewServer() *Server {
	return &Server{serviceMap: make(map[string]*service)}
}

// Is this an exported - upper case - name?
func isExported(name string) bool {
	rune, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(rune)
}

func (server *Server) Register(rcvr interface{}) error {
	server.lock.Lock()
	defer server.lock.Unlock()
	if server.serviceMap == nil {
		server.serviceMap = make(map[string]*service)
	}
	s := new(service)
	s.typ = reflect.TypeOf(rcvr)
	s.rcvr = reflect.ValueOf(rcvr)
	sname := reflect.Indirect(s.rcvr).Type().Name()
	if sname == "" {
		s := "searpc.Register: no service name for type " + s.typ.String()
		log.Print(s)
		return errors.New(s)
	}
	if !isExported(sname) {
		s := "searpc.Register: type " + sname + " is not exported"
		log.Print(s)
		return errors.New(s)
	}
	if _, present := server.serviceMap[sname]; present {
		return errors.New("searpc: service already defined: " + sname)
	}
	s.name = sname

	// Install the methods
	s.method = suitableMethods(s.typ, true)

	if len(s.method) == 0 {
		str := ""

		// To help the user, see if a pointer receiver would work.
		method := suitableMethods(reflect.PtrTo(s.typ), false)
		if len(method) != 0 {
			str = "searpc.Register: type " + sname + " has no exported methods of suitable type (hint: pass a pointer to value of that type)"
		} else {
			str = "searpc.Register: type " + sname + " has no exported methods of suitable type"
		}
		log.Print(str)
		return errors.New(str)
	}
	server.serviceMap[s.name] = s
	return nil
}

// suitableMethods returns suitable Rpc methods of typ, it will report
// error using log if reportErr is true.
func suitableMethods(typ reflect.Type, reportErr bool) map[string]*reflect.Method {
	methods := make(map[string]*reflect.Method)
	for m := 0; m < typ.NumMethod(); m++ {
		method := typ.Method(m)
		mtype := method.Type
		mname := method.Name
		// Method must be exported.
		if method.PkgPath != "" {
			continue
		}
		// Method needs one out.
		if mtype.NumOut() != 1 {
			if reportErr {
				log.Println("method", mname, "has wrong number of outs:", mtype.NumOut())
			}
			continue
		}
		// The return type of the method must be error.
		if returnType := mtype.Out(0); returnType != typeOfResult {
			if reportErr {
				log.Println("method", mname, "returns", returnType.String(), "not error")
			}
			continue
		}

		regMethod := method
		methods[mname] = &regMethod
	}
	return methods
}

const (
	ServiceNotFoundError  = 501
	FunctionNotFoundError = 500
	ParseJSONError        = 511
	ParameterError        = 512
)

func (server *Server) Call(serviceName string, callStr []byte) (retStr []byte) {
	var res Result
	var errStr string
	var errCode int

	service := server.serviceMap[serviceName]
	if service == nil {
		res = Result{ErrCode: ServiceNotFoundError, ErrMsg: "Cannot find service " + serviceName}
		retStr, _ = json.Marshal(res)
		return
	}

	var data interface{}
	parseErr := json.Unmarshal(callStr, &data)
	if parseErr != nil {
		errStr = "Failed to parse call string:" + parseErr.Error()
		errCode = ParseJSONError
		log.Println(errStr)
		res = Result{ErrCode: errCode, ErrMsg: errStr}
		retStr, _ = json.Marshal(res)
		return
	}

	array, ok := data.([]interface{})
	if !ok || len(array) == 0 {
		errStr = "Invalid call string format"
		errCode = ParseJSONError
		log.Println(errStr)
		res = Result{ErrCode: errCode, ErrMsg: errStr}
		retStr, _ = json.Marshal(res)
		return
	}

	funcName, ok := array[0].(string)
	if !ok {
		errStr = "Invalid call string format"
		errCode = ParseJSONError
		log.Println(errStr)
		res = Result{ErrCode: errCode, ErrMsg: errStr}
		retStr, _ = json.Marshal(res)
		return
	}

	method := service.method[funcName]
	if method == nil {
		errStr = "Cannot find function " + funcName
		errCode = FunctionNotFoundError
		log.Println(errStr)
		res = Result{ErrCode: errCode, ErrMsg: errStr}
		retStr, _ = json.Marshal(res)
		return
	}

	mtype := method.Type
	if mtype.NumIn() != len(array)-1 {
		errStr = "Parameters mismatch"
		errCode = ParameterError
		log.Println(errStr)
		res = Result{ErrCode: errCode, ErrMsg: errStr}
		retStr, _ = json.Marshal(res)
		return
	}

	params := []reflect.Value{service.rcvr}
	for i := 1; i < len(array); i++ {
		params = append(params, reflect.ValueOf(array[i]))
	}
	errValue := method.Func.Call(params)
	res = errValue[0].Interface().(Result)

	retStr, _ = json.Marshal(res)
	return
}
