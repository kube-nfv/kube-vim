package image

import "testing"


func TestFoo(t *testing.T) {
    c := CdiController{}
    c.GetDv(WithName2("test"), WithNamespace2("aaa"))
}
