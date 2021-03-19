package recover

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"time"

	"github.com/sirupsen/logrus"
)

type GoRun struct {
	limit     int
	timeout   time.Duration
	isBlock   bool
	limitCh   chan struct{}
	routineCh chan *routine
}

type routine struct {
	function interface{}
	params   []interface{}
}

var goRun = NewGoRun(-1, 0, false)

func NewGoRun(limit int, timeout time.Duration, isBlock bool) *GoRun {
	var limitCh chan struct{}
	if limit > 0 {
		limitCh = make(chan struct{}, limit)
	}

	gorun := &GoRun{
		limit:     limit,
		timeout:   timeout,
		isBlock:   isBlock,
		limitCh:   limitCh,
		routineCh: make(chan *routine),
	}

	go gorun.init()
	return gorun
}

func Recover(format string, args ...interface{}) {
	if e := recover(); e != nil {
		logstack(e, format, args...)
	}
}

func logstack(e interface{}, format string, args ...interface{}) {
	if e != nil {
		const size = 64 << 10
		buf := make([]byte, size)
		buf = buf[:runtime.Stack(buf, false)]
		err, ok := e.(error)
		if !ok {
			err = fmt.Errorf("%v", e)
		}
		logrus.WithError(err).WithField("stack", "...\n"+string(buf)).Errorf(format, args...)
	}
}

func Go(function interface{}, params ...interface{}) {
	goRun.Go(function, params...)
}

func (this *GoRun) Go(function interface{}, params ...interface{}) {
	this.routineCh <- &routine{function: function, params: params}
}

func (this *GoRun) Close() {
	close(this.routineCh)
}

func (this *GoRun) run(function interface{}, cancelFunc context.CancelFunc, params ...interface{}) {
	go func() {
		defer func() {
			if e := recover(); e != nil {
				logstack(e, "panic")
			}
			if cancelFunc != nil {
				cancelFunc()
			}
		}()
		f := reflect.ValueOf(function)
		if !f.IsValid() || f.IsNil() || f.Kind() != reflect.Func {
			return
		}
		vs := make([]reflect.Value, len(params))
		for idx, param := range params {
			vs[idx] = reflect.ValueOf(param)
		}
		f.Call(vs)
	}()
}

func (this *GoRun) runLimitRoutine(r *routine) {
	if this.isBlock && this.limitCh != nil {
		this.limitCh <- struct{}{}
	}
	go func(r *routine) {
		if this.limitCh != nil {
			if !this.isBlock {
				this.limitCh <- struct{}{}
			}
			ctx, cancel := context.WithTimeout(context.Background(), this.timeout)
			this.run(r.function, cancel, r.params...)
			select {
			case <-ctx.Done():
				<-this.limitCh
			}
		} else {
			this.run(r.function, nil, r.params...)
		}
	}(r)
}

func (this *GoRun) init() {
	for r := range this.routineCh {
		this.runLimitRoutine(r)
	}
	if this.limitCh != nil {
		close(this.limitCh)
	}
}
