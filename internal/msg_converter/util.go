package message

import (
	"encoding/binary"
	"fmt"
	"time"
)

type PathMap[T WithIdentifier] struct {
	m map[string]T
	a []T
}

func NewPathMap[T WithIdentifier]() *PathMap[T] {
	return &PathMap[T]{m: make(map[string]T)}
}

func (p *PathMap[T]) Add(t T) error {
	if t.getId() != len(p.a) {
		return fmt.Errorf("the item must be add in sequence")
	}
	p.a = append(p.a, t)
	p.m[t.getKey()] = t
	return nil
}

func (p *PathMap[T]) GetValue(key string) (T, bool) {
	t, ok := (*p).m[key]
	return t, ok
}

func (p *PathMap[T]) GetKey(id int) (string, bool) {
	if id < 0 || id >= len(p.a) {
		return "", false
	}
	return (p.a[id]).getKey(), true
}

func CapitalUpperCase(str string) string {
	if 'a' <= str[0] && str[0] <= 'z' {
		str = string(str[0]+'A'-'a') + str[1:]
	}
	return str
}

func MapBinaryToFloat(buffer *[]byte, field *Field, offset int) float64 {
	buf := *buffer
	tmp := make([]byte, 0, 4)
	
	for i := 0; i < 4-field.Length; i++ {
		tmp = append(tmp, 0x00)
	}
	tmp = append(tmp, buf[offset:offset+field.Length]...)
	
	param := binary.BigEndian.Uint32(tmp)
	
	ratioMax := 1<<(field.Length*8) - 1
	
	return field.RangeMin + (field.RangeMax-field.RangeMin)*float64(param)/float64(ratioMax)
}

func MapFloatToBinary(value float64, field *Field) []byte {
	percentage := (value - field.RangeMin) / (field.RangeMax - field.RangeMin)
	ratioMax := 1<<(field.Length*8) - 1
	buf := []byte{0, 0, 0, 0}
	binary.BigEndian.PutUint32(buf, uint32(percentage*float64(ratioMax)))
	
	return buf[len(buf)-field.Length:]
}

func Log2(arrLen int) int {
	i, remain := 0, 0
	for arrLen > 1 {
		if arrLen%2 == 1 {
			remain = 1
		}
		arrLen /= 2
		i++
	}
	return i + remain
}


func ByteToBoolSlice(b byte) []bool {
	var slice []bool
	for i := 0; i < 8; i++ {
		if b&(1<<uint(7-i)) != 0 {
			slice = append(slice, true)
		} else {
			slice = append(slice, false)
		}
	}
	return slice
}


func BytesToBoolSlice(data []byte) []bool {
	var boolSlice []bool
	for _, b := range data {
		boolSlice = append(boolSlice, ByteToBoolSlice(b)...)
	}
	return boolSlice
}


func BoolSliceToBytes(boolSlice []bool) []byte {
	var bytes []byte
	for i := 0; i < len(boolSlice); i += 8 {
		var byteValue byte
		for j := 0; j < 8 && i+j < len(boolSlice); j++ {
			if boolSlice[i+j] {
				byteValue |= 1 << uint(7-j)
			}
		}
		bytes = append(bytes, byteValue)
	}
	return bytes
}

const TAK_FMT = "2006-01-02T15:04:05.999Z"

func GetCurrentFormatTime() string {
	return time.Now().UTC().Format(TAK_FMT)
}
