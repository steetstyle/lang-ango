package main

import (
	"fmt"
	"github.com/yourorg/lang-ango/pkg/processor"
	"unsafe"
)

func main() {
	var e processor.DBEvent
	fmt.Printf("DBEvent size: %d\n", unsafe.Sizeof(e))
	fmt.Printf("DBEvent offset of Query: %d\n", unsafe.Offsetof(e.Query))
	fmt.Printf("DBEvent offset of QueryLen: %d\n", unsafe.Offsetof(e.QueryLen))
}
