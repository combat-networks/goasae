package message

import (
	"encoding/binary"
	"fmt"
	"github.com/kdudkov/goasae/pkg/cot"
	"strings"
)

type ArrayStatus struct {
	index      int
	size       int
	arr        *[]*cot.Node
	parentNode *cot.Node
	parent     *ArrayStatus
}

func (s *ArrayStatus) GetSize() int {
	return s.size
}

func (s *ArrayStatus) SetParentNode(p *cot.Node) {
	s.parentNode = p
}

func (s *ArrayStatus) GetParentNode() *cot.Node {
	return s.parentNode
}

func (s *ArrayStatus) SetNodesArray(arr *[]*cot.Node) {
	s.arr = arr
	s.size = len(*s.arr)
}

func (s *ArrayStatus) GetParent() *ArrayStatus {
	return s.parent
}

func (s *ArrayStatus) GetCurrentNode() *cot.Node {
	if !s.IsInBounds() {
		return nil
	}
	if s.arr != nil {
		return (*s.arr)[s.index]
	}
	return nil
}

func (s *ArrayStatus) GetCurrentNodeByName(name string) *cot.Node {
	if !s.IsInBounds() {
		return nil
	}
	if s.arr != nil {
		return (*s.arr)[s.index]
	}
	if name != "" && s.parentNode != nil {
		nodes := s.parentNode.GetAll(name)
		s.SetNodesArray(&nodes)
		if s.IsInBounds() {
			return (*s.arr)[s.index]
		}
	}
	return nil
}

func (s *ArrayStatus) First() {
	s.index = 0
}

func (s *ArrayStatus) Next() {
	s.index++
}

func (s *ArrayStatus) IsFirst() bool {
	return s.index == 0
}

func (s *ArrayStatus) IsLast() bool {
	return s.index == s.size-1
}

func (s *ArrayStatus) IsInBounds() bool {
	return 0 <= s.index && s.index < s.size
}


func (s *ArrayStatus) InitFormBinary(c *Converter, field *Field) error {
	if field.Type != "array" {
		return fmt.Errorf("current field is not array")
	}
	
	_, ok := Refs[field.Name]
	if !ok {
		return fmt.Errorf("the array field should be a reference")
	}
	lSize := 0
	
	switch field.SizeLimit {
	case 0x7F:
		lSize = 1
		s.size = int((*c.buffer)[c.offset])
	case 0x7FFF:
		lSize = 2
		s.size = int(binary.BigEndian.Uint16((*c.buffer)[c.offset : c.offset+lSize]))
	default:
		s.size = field.SizeLimit
	}
	c.offset += lSize
	return nil
}

func (s *ArrayStatus) InitFormEvent(c *Converter, f *Field) error {
	parent, nameAttr, err := getCotParentNodeFromCot(c, false, s)
	if err != nil {
		return err
	}
	nameattr := strings.Split(nameAttr, ".")
	if len(nameattr) != 2 {
		return fmt.Errorf("invalid name attribute")
	}
	name := nameattr[0]
	nodes := parent.GetAll(name)
	s.SetNodesArray(&nodes)
	return nil
}
