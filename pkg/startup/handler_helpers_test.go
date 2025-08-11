package startup

import (
	"testing"
)

// drain channel to slice
func drain(ch <-chan interface{}) []interface{} {
	var out []interface{}
	for {
		select {
		case v := <-ch:
			out = append(out, v)
		default:
			return out
		}
	}
}

func TestCacheResourceHandler_Add(t *testing.T) {
	calls := make(chan interface{}, 1)
	h := cacheResourceHandler(func(obj interface{}) {
		calls <- obj
	})
	obj := &struct{ name string }{"add"}
	h.OnAdd(obj, false)
	got := drain(calls)
	if len(got) != 1 {
		t.Fatalf("expected 1 call, got %d", len(got))
	}
	if got[0] != obj {
		t.Fatalf("expected obj pointer %p, got %p", obj, got[0])
	}
}

func TestCacheResourceHandler_Update_UsesNewObjOnly(t *testing.T) {
	calls := make(chan interface{}, 1)
	h := cacheResourceHandler(func(obj interface{}) {
		calls <- obj
	})
	oldObj := &struct{ v int }{1}
	newObj := &struct{ v int }{2}
	h.OnUpdate(oldObj, newObj)
	got := drain(calls)
	if len(got) != 1 {
		t.Fatalf("expected 1 call, got %d", len(got))
	}
	if got[0] != newObj {
		t.Fatalf("expected newObj %p, got %p", newObj, got[0])
	}
}

func TestCacheResourceHandler_Delete_NoInvocation(t *testing.T) {
	calls := make(chan interface{}, 1)
	h := cacheResourceHandler(func(obj interface{}) {
		calls <- obj
	})
	obj := &struct{ id int }{5}
	// Delete should be a no-op (DeleteFunc nil)
	h.OnDelete(obj)
	got := drain(calls)
	if len(got) != 0 {
		t.Fatalf("expected 0 calls, got %d", len(got))
	}
}

func TestCacheResourceHandler_Sequence(t *testing.T) {
	calls := make(chan interface{}, 3)
	h := cacheResourceHandler(func(obj interface{}) {
		calls <- obj
	})
	a := &struct{ s string }{"a"}
	b := &struct{ s string }{"b"}
	c := &struct{ s string }{"c"}
	h.OnAdd(a, false) // expect record a
	h.OnUpdate(a, b)  // expect record b
	h.OnDelete(c)     // no record
	h.OnUpdate(b, c)  // expect record c
	got := drain(calls)
	if len(got) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(got))
	}
	exp := []interface{}{a, b, c}
	for i := range exp {
		if got[i] != exp[i] {
			t.Fatalf("at %d expected %p, got %p", i, exp[i], got[i])
		}
	}
}
