package message

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"github.com/google/uuid"
	"github.com/kdudkov/goasae/pkg/cot"
	"hash/fnv"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

var fieldConverters = make(map[string]FieldConverter)

func init() {
	fieldConverters["subNetTypeConverter"] = &SubNetTypeConverter{}
	fieldConverters["messageTypeConverter"] = &MsgTypeConverter{}
	fieldConverters["roleGroupConverter"] = &RoleGroupConverter{}
	fieldConverters["lengthLimitedFloatConverter"] = &LengthLimitedFloatConverter{}
	fieldConverters["pointLengthLimitedFloatConverter"] = &PointLengthLimitedFloatConverter{}
	fieldConverters["uidConverter"] = &UidConverter{}
	fieldConverters["chatUidConverter"] = &ChatUidConverter{}
	fieldConverters["uidCodecConverter"] = &UidGroupCodecConverter{}
	fieldConverters["remarksCodecConverter"] = &RemarksCodecConverter{}
	fieldConverters["stringConverter"] = &StringConverter{}
	fieldConverters["stringReflectConverter"] = &StringReflectConverter{}
	fieldConverters["colorConverter"] = &ColorConverter{}
	fieldConverters["floatConverter"] = &FloatConverter{}
	fieldConverters["intConverter"] = &IntConverter{}
	fieldConverters["constConverter"] = &ConstConverter{}
	fieldConverters["constzMistTitleConverter"] = &ConstzMistTitleConverter{}
	fieldConverters["placeHolderConverter"] = &PlaceHolderConverter{}
	fieldConverters["msgLengthConverter"] = &MsgLengthConverter{}
	fieldConverters["msgCheckSumConverter"] = &MsgCheckSumConverter{}
	fieldConverters["booleanConverter"] = &BooleanConverter{}
	fieldConverters["routeMaskConverter"] = &RouteMaskConverter{}
	fieldConverters["maskConverter"] = &SingleChoiceMaskConverter{}
	fieldConverters["multiChoiceMaskConverter"] = &MultiChoiceMaskConverter{}
	fieldConverters["zMistsMultiChoiceMaskConverter"] = &ZMistsMultiChoiceMaskConverter{}
	fieldConverters["booleanMaskConverter"] = &BooleanMaskConverter{}
	fieldConverters["pathIconConverter"] = &PathIconConverter{}
	fieldConverters["linkPointConverter"] = &LinkPointConverter{}
	fieldConverters["routeLinkPointConverter"] = &RouteLinkPointConverter{}
	fieldConverters["obstaclesConverter"] = &ObstaclesConverter{}
}


type FieldConverter interface {
	
	toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error)
	
	toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error)
}

func getCotNodeByFieldName(converter *Converter, autoFill bool, s *ArrayStatus) (*cot.Node, error) {
	
	parent, nameAttr, err := getCotParentNodeFromCot(converter, autoFill, s)
	if err != nil {
		return nil, err
	}
	nameattr := strings.Split(nameAttr, ".")
	if len(nameattr) != 2 {
		return nil, fmt.Errorf("error: the path is in an invalid format, the dot should only appear once in the last section")
	}
	name, _ := nameattr[0], nameattr[1]
	expectedLen := 1
	if s != nil {
		expectedLen = s.GetSize()
	}
	nodes := parent.GetAll(name)
	nodesLen := len(nodes)
	if nodesLen < expectedLen { 
		if !autoFill {
			return nil, fmt.Errorf("error: the number of node [%s] is [%d], less than expected count [%d] and should not be filled", name, nodesLen, expectedLen)
		}
		filler, ok := fillerMap[name]
		if !ok {
			filler = fillerMap["default"]
		}
		delta := expectedLen - nodesLen
		for i := 0; i < delta; i++ {
			_, err := filler(converter, parent, name)
			if err != nil {
				return nil, err
			}
		}
	}
	if s == nil || s.GetSize() == 1 {
		return parent.GetFirst(name), nil
	} else {
		if s.IsFirst() && s.GetSize() == len(parent.GetAll(name)) {
			nodes := parent.GetAll(name)
			s.SetNodesArray(&nodes)
		}
		return s.GetCurrentNode(), nil
	}
}

func insertAttrToCot(converter *Converter, val string, s *ArrayStatus) (*cot.Node, error) {
	if strings.HasPrefix(val, "$") { 
		name := converter.curField.Name
		converter.curField.Name = strings.TrimPrefix(val, "$")
		attr, err := getAttrFromCot(converter, s)
		if err != nil {
			return nil, fmt.Errorf("the refed field [%s] must appear before ref", val)
		}
		
		converter.curField.Name = name
		val = attr
	}
	node, err := getCotNodeByFieldName(converter, true, s)
	if err != nil {
		return nil, err
	}
	attr := strings.Split(converter.curField.Name, ".")[1]
	if attr == "" {
		node.Content = val
	} else {
		node.Attrs = append(node.Attrs, xml.Attr{
			Name:  xml.Name{Local: attr},
			Value: val,
		})
	}
	return node, nil
}

func getAttrFromCot(converter *Converter, s *ArrayStatus) (string, error) {
	
	node, err := getCotNodeByFieldName(converter, false, s)
	if err != nil {
		if converter.curField.Value != "" {
			return converter.curField.Value, nil
		}
		return "", err
	}
	attr := strings.Split(converter.curField.Name, ".")[1]
	if attr == "" {
		return node.Content, nil
	} else {
		return node.GetAttr(attr), nil
	}
}

func getCotParentNodeFromCot(converter *Converter, autoFill bool, s *ArrayStatus) (*cot.Node, string, error) {
	
	nodePath := strings.Split(converter.curField.Name, "/")
	if nodePath[0] != "detail" {
		return nil, "", fmt.Errorf("error: most of the path of the attr should begin with [detail]")
	}
	
	if converter.cotEvent.Detail == nil {
		if autoFill {
			converter.cotEvent.Detail = &cot.Node{}
		} else {
			return nil, "", fmt.Errorf("error: the detail node is empty so that nothing can be found")
		}
	}
	curNode := converter.cotEvent.Detail
	pathLen := len(nodePath)                     
	for index := 1; index < pathLen-1; index++ { 
		path := nodePath[index]
		
		if strings.Contains(path, ".") {
			return nil, "", fmt.Errorf("error: the path is in an invalid format, attribute is NOT appear in the last section of the path")
		}
		
		if !curNode.Has(path) {
			if !autoFill {
				return nil, "", fmt.Errorf("error: the node[%s] is absent and should not be filled", path)
			}
			filler, ok := fillerMap[path]
			if !ok {
				filler = fillerMap["default"]
			}
			
			_, err := filler(converter, curNode, path)
			if err != nil {
				return nil, "", err
			}
		}
		curNode = curNode.GetFirst(path)
	}
	return curNode, nodePath[pathLen-1], nil
}

type PathIconConverter struct{}

func (p *PathIconConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	data := (*converter.buffer)[converter.offset : converter.offset+converter.curField.Length]
	pathId := binary.BigEndian.Uint16(data[0:2])
	iconId := binary.BigEndian.Uint16(data[2:4])
	iconPath, ok := IconPathMap.GetKey(int(pathId))
	if !ok {
		return nil, 0, fmt.Errorf("error: the iconPath is empty")
	}
	iconName, ok := IconFileMap.GetKey(int(iconId))
	if !ok {
		return nil, 0, fmt.Errorf("error: the iconName is empty")
	}
	iconsetPath := iconPath + "/" + iconName
	_, err := insertAttrToCot(converter, iconsetPath, nil)
	if err != nil {
		return nil, 0, err
	}
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}

func (p *PathIconConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	attr, err := getAttrFromCot(converter, s)
	if err != nil {
		return nil, 0, err
	}
	index := strings.LastIndex(attr, "/")
	iconPath := attr[:index]
	val, ok := IconPathMap.GetValue(iconPath)
	if !ok {
		return nil, 0, fmt.Errorf("error: the iconPath is not indexed")
	}
	*converter.buffer = binary.BigEndian.AppendUint16(*converter.buffer, uint16(val.getId()))
	iconName := attr[index+1:]
	val2, ok := IconFileMap.GetValue(iconName)
	if !ok {
		return nil, 0, fmt.Errorf("error: the iconName is not indexed")
	}
	*converter.buffer = binary.BigEndian.AppendUint16(*converter.buffer, uint16(val2.getId()))
	return converter.buffer, converter.offset + converter.curField.Length, nil
}

var routeMasks = map[string]map[string]int{
	"method":    {"Driving": 0, "Walking": 1, "Flying": 2, "Swimming": 3, "Watercraft": 4},
	"direction": {"Infil": 0, "Exfil": 1},
	"routetype": {"Primary": 0, "Secondary": 1},
	"order":     {"Ascending Check Points": 0, "Descending Check Points": 1},
}

var booleanMasks = []string{"false", "true"}

type BooleanMaskConverter struct{}

func (c *BooleanMaskConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	converter.curField.Selections = booleanMasks
	return fieldConverters["maskConverter"].toEventField(s, converter)
}

func (c *BooleanMaskConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	converter.curField.Selections = booleanMasks
	return fieldConverters["maskConverter"].toBinaryField(s, converter)
}

type SingleChoiceMaskConverter struct{}

func (c *SingleChoiceMaskConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	
	
	masks := (*converter.buffer)[converter.offset : converter.offset+converter.curField.SizeLimit]
	
	var placeholder []byte
	for i := 0; i < 4-converter.curField.SizeLimit; i++ {
		placeholder = append(placeholder, 0x00)
	}
	placeholder = append(placeholder, masks...)
	
	imask := binary.BigEndian.Uint32(placeholder)
	
	selectionsLen := Log2(len(converter.curField.Selections))
	
	imask >>= converter.curField.RelativeOffset
	
	m2 := uint32(1<<selectionsLen - 1) 
	
	imask &= m2
	_, err := insertAttrToCot(converter, converter.curField.Selections[imask], s)
	if err != nil {
		return nil, 0, err
	}
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}

func (c *SingleChoiceMaskConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	attr, err := getAttrFromCot(converter, s)
	if attr == "" { 
		attr = converter.curField.Selections[0]
	}
	if err != nil {
		return nil, 0, err
	}
	for index, selection := range converter.curField.Selections {
		if selection == attr {
			var masks []byte
			if converter.curField.RelativeOffset == 0 { 
				
				converter.offset = len(*converter.buffer)
				for i := 0; i < converter.curField.SizeLimit; i++ {
					*converter.buffer = append(*converter.buffer, 0x00)
				}
			}
			
			
			masks = (*converter.buffer)[converter.offset : converter.offset+converter.curField.SizeLimit]
			
			mask := index << converter.curField.RelativeOffset

			
			var placeholder []byte
			for i := 0; i < 4-converter.curField.SizeLimit; i++ {
				placeholder = append(placeholder, 0x00)
			}
			placeholder = append(placeholder, masks...)

			
			imask := int(binary.BigEndian.Uint32(placeholder))
			
			imask |= mask

			var result []byte
			result = binary.BigEndian.AppendUint32(result, uint32(imask))

			
			for i := 4 - converter.curField.SizeLimit; i < 4; i++ {
				bufIndex := i + converter.curField.SizeLimit - 4
				(*converter.buffer)[converter.offset+bufIndex] = result[i]
			}

			return converter.buffer, converter.offset + converter.curField.Length, nil
		}
	}
	return nil, 0, fmt.Errorf("error: cannot find mapping for value [%s]", attr)
}


type MultiChoiceMaskConverter struct{}

func (c *MultiChoiceMaskConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	bytes := (*converter.buffer)[converter.offset : converter.offset+converter.curField.SizeLimit]
	exists := BytesToBoolSlice(bytes)
	str := ""
	for i, exist := range exists {
		if i >= len(converter.curField.Selections) {
			break
		}
		if exist {
			str += converter.curField.Selections[i] + " "
		}
	}
	_, err := insertAttrToCot(converter, str[:len(str)-1], s)
	if err != nil {
		return nil, 0, err
	}
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}

func (c *MultiChoiceMaskConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	attr, err := getAttrFromCot(converter, s)
	if err != nil {
		return nil, 0, err
	}
	exists := make([]bool, converter.curField.SizeLimit*8)
	for i, selection := range converter.curField.Selections {
		exists[i] = strings.Contains(attr, selection)
	}
	bytes := BoolSliceToBytes(exists)
	*converter.buffer = append(*converter.buffer, bytes...)
	return converter.buffer, 0, nil
}

var zMistsMasks = map[string]map[string]int{
	"Bleeding":       {"Minimal": 0, "Massive": 1},
	"Airway":         {"Has Airway": 0, "No Airway": 1},
	"Pulse Radial":   {"Has Radial": 0, "No Radial": 1},
	"Pulse Strength": {"Strong/Steady": 0, "Weak/Rapid": 1},
	"Skin":           {"Warm/Moist": 0, "Cold/Clammy": 1},
	"Pupils":         {"Constricted": 0, "Dilated": 1, "Normal": 2},
	"Breathing":      {"Labored": 0, "Normal": 1, "Absent": 2},
}
var zMistsIndex = []string{"Bleeding", "Airway", "Pulse Radial", "Pulse Strength", "Skin", "Pupils", "Breathing"}

type ZMistsMultiChoiceMaskConverter struct{}

func (c *ZMistsMultiChoiceMaskConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	data := (*converter.buffer)[converter.offset : converter.offset+converter.curField.SizeLimit]
	exists := BytesToBoolSlice(data)
	buffer := bytes.Buffer{}
	for i := 0; i < 5; i++ { 
		index := 0
		if exists[i] {
			index += 1
		}
		for itm, idx := range zMistsMasks[zMistsIndex[i]] {
			if idx == index {
				buffer.WriteString(zMistsIndex[i])
				buffer.WriteString(": ")
				buffer.WriteString(itm)
				buffer.WriteString("\n")
				break
			}
		}
	}
	for i := 5; i < 9; i += 2 { 
		index := 0
		if exists[i] {
			index += 1
		}
		if exists[i+1] {
			index += 2
		}
		zidx := zMistsIndex[5+(i-5)/2]
		for itm, idx := range zMistsMasks[zidx] {
			if idx == index {
				buffer.WriteString(zidx)
				buffer.WriteString(": ")
				buffer.WriteString(itm)
				buffer.WriteString("\n")
				break
			}
		}
	}
	buffer.Truncate(buffer.Len() - 1)
	_, err := insertAttrToCot(converter, buffer.String(), s)
	if err != nil {
		return nil, 0, err
	}
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}

func (c *ZMistsMultiChoiceMaskConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	attr, err := getAttrFromCot(converter, s)
	if err != nil {
		return nil, 0, err
	}
	attrs := strings.Split(attr, "\n")

	masks := make([]bool, converter.curField.SizeLimit*8)
	for _, attr := range attrs {
		kv := strings.SplitN(attr, ": ", 2)
		if len(kv) != 2 {
			continue
		}
		key, value := kv[0], kv[1]
		i := zMistsMasks[key][value]
		index := slices.Index(zMistsIndex, key)
		if index < 5 {
			masks[index] = i == 1
		} else {
			masks[5+(index-5)*2] = i%2 == 1 
			masks[5+(index-5)*2+1] = i >= 2 
		}
	}
	data := BoolSliceToBytes(masks)
	*converter.buffer = append(*converter.buffer, data...)
	return converter.buffer, 0, nil
}

type RouteMaskConverter struct{}

func (c *RouteMaskConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	mask := 0x00
	name := converter.curField.Name
	baseName := "detail/link_attr."
	for _, attr := range []string{"method", "direction", "routetype", "order"} {
		mask = mask << 1
		converter.curField.Name = baseName + attr
		fieldAttr, err := getAttrFromCot(converter, s)
		if err != nil {
			return nil, 0, err
		}
		i, ok := routeMasks[attr][fieldAttr]
		if !ok {
			return nil, 0, fmt.Errorf("error: the route mask attribute does not exist")
		}
		mask |= i
	}
	converter.curField.Name = name
	*converter.buffer = append(*converter.buffer, byte(mask))
	return converter.buffer, 0, nil
}

func (c *RouteMaskConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	mask := (*converter.buffer)[converter.offset]
	name := converter.curField.Name
	baseName := "detail/link_attr."
	for _, attr := range []string{"order", "routetype", "direction", "method"} {
		i := byte(0)
		if attr == "method" {
			i = mask & 0x07
		} else {
			i = mask & 0x01
		}
		masks, ok := routeMasks[attr]
		if !ok {
			return nil, 0, fmt.Errorf("error: the route mask [%s] does not exist", attr)
		}
		for key, value := range masks {
			if byte(value) == i {
				converter.curField.Name = baseName + attr
				_, err := insertAttrToCot(converter, key, s)
				if err != nil {
					return nil, 0, err
				}
			}
		}
		mask = mask >> 1
	}
	converter.curField.Name = name
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}

type BooleanConverter struct{}

func (c *BooleanConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	buf := *converter.buffer
	b := buf[converter.offset]
	var err error
	if b == 0 {
		_, err = insertAttrToCot(converter, "false", s)
	} else {
		_, err = insertAttrToCot(converter, "true", s)
	}
	return converter.cotEvent, converter.offset + converter.curField.Length, err
}

func (c *BooleanConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	attr, err := getAttrFromCot(converter, s)
	if err != nil {
		return nil, 0, err
	}
	if attr == "true" {
		*converter.buffer = append(*converter.buffer, 0x01)
	} else {
		*converter.buffer = append(*converter.buffer, 0x00)
	}
	return converter.buffer, 0, nil
}

type MsgLengthConverter struct{}

func (c *MsgLengthConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	buflen, bufLen := (*converter.buffer)[converter.curField.Offset:converter.curField.Offset+converter.curField.Length], 0
	switch converter.curField.Length {
	case 1:
		bufLen = int(buflen[0])
	case 2:
		bufLen = int(binary.BigEndian.Uint16(buflen))
	case 4:
		bufLen = int(binary.BigEndian.Uint32(buflen))
	}
	if len(*converter.buffer) < bufLen {
		return nil, 0, fmt.Errorf("error: the message is incomplete")
	}
	
	return converter.cotEvent, converter.offset, nil
}

func (c *MsgLengthConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	var msgLen []byte
	data := *converter.buffer
	switch converter.curField.Length {
	case 1:
		msgLen = append(msgLen, uint8(len(data)))
	case 2:
		msgLen = binary.BigEndian.AppendUint16(msgLen, uint16(len(data)))
	case 4:
		msgLen = binary.BigEndian.AppendUint32(msgLen, uint32(len(data)))
	}
	for i := 0; i < converter.curField.Length; i++ {
		data[converter.curField.Offset+i] = msgLen[i]
	}
	return converter.buffer, converter.offset, nil
}

type MsgCheckSumConverter struct{}

func (c *MsgCheckSumConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	buf := *converter.buffer
	msgLen := len(buf)

	var checkSum []byte
	for _ = range converter.curField.Length {
		checkSum = append(checkSum, 0x00)
	}
	fieldLen := converter.curField.Length
	
	loops := msgLen / fieldLen
	
	remain := msgLen % fieldLen
	for loop := 0; loop < loops; loop++ {
		for i := 0; i < fieldLen; i++ {
			checkSum[i] ^= buf[loop*fieldLen+i]
		}
	}
	
	for i := 0; i < remain; i++ {
		checkSum[i] ^= buf[loops*fieldLen+i]
	}
	for _, cs := range checkSum {
		if cs != 0x00 {
			return nil, 0, fmt.Errorf("message checksum failed")
		}
	}
	return converter.cotEvent, converter.offset, nil
}

func (c *MsgCheckSumConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	var result []byte
	fieldLen := converter.curField.Length
	for i := 0; i < fieldLen; i++ {
		result = append(result, 0x00)
	}
	buf := *converter.buffer
	msgLen := len(buf)
	
	loops := msgLen / fieldLen
	
	remain := msgLen % fieldLen

	for loop := 0; loop < loops; loop++ {
		for i := 0; i < fieldLen; i++ {
			result[i] ^= buf[loop*fieldLen+i]
		}
	}
	
	for i := 0; i < remain; i++ {
		result[i] ^= buf[loops*fieldLen+i]
	}
	
	for i := 0; i < converter.curField.Length; i++ {
		buf[converter.curField.Offset+i] = result[i]
	}
	return converter.buffer, converter.offset, nil
}


type PlaceHolderConverter struct{}

func (p *PlaceHolderConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}

func (p *PlaceHolderConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	length := converter.curField.Length
	for i := 0; i < length; i++ {
		*converter.buffer = append(*converter.buffer, 0x00)
	}
	return converter.buffer, length, nil
}

type ConstConverter struct{}

func (c *ConstConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	
	_, err := insertAttrToCot(converter, converter.curField.Value, s)
	if err != nil {
		return nil, 0, err
	}
	return converter.cotEvent, converter.offset, nil
}

func (c *ConstConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	return converter.buffer, 0, nil
}

type ConstzMistTitleConverter struct{}

func (c *ConstzMistTitleConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	zTitle := fmt.Sprintf("ZMIST%d", s.index+1)
	_, err := insertAttrToCot(converter, zTitle, s)
	if err != nil {
		return nil, 0, err
	}
	return converter.cotEvent, converter.offset, nil
}

func (c *ConstzMistTitleConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	return converter.buffer, 0, nil
}

type FloatConverter struct{}

func (c *FloatConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	bytes := (*converter.buffer)[converter.offset : converter.offset+converter.curField.Length]
	f := math.Float32frombits(binary.BigEndian.Uint32(bytes))
	radius := strconv.FormatFloat(float64(f), 'f', -1, 32)
	_, err := insertAttrToCot(converter, radius, s)
	if err != nil {
		return nil, 0, err
	}
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}

func (c *FloatConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	attr, err := getAttrFromCot(converter, s)
	if attr == "" {
		if converter.curField.Value != "" { 
			attr = converter.curField.Value
		} else {
			attr = "0.0"
		}
	}
	if err != nil {
		return nil, 0, err
	}
	f, err := strconv.ParseFloat(attr, 32)
	if err != nil {
		return nil, 0, err
	}
	*converter.buffer = binary.BigEndian.AppendUint32(*converter.buffer, math.Float32bits(float32(f)))
	return converter.buffer, converter.curField.Length, nil
}

type IntConverter struct{}

func (c *IntConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	data := (*converter.buffer)[converter.offset : converter.offset+converter.curField.Length]
	i := 0
	switch converter.curField.Length { 
	case 1:
		i = int(int8(data[0]))
	case 2:
		i = int(int16(binary.BigEndian.Uint16(data)))
	case 4:
		i = int(int32(binary.BigEndian.Uint32(data)))
	}
	_, err := insertAttrToCot(converter, strconv.Itoa(i), s)
	if err != nil {
		return nil, 0, err
	}
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}

func (c *IntConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	attr, err := getAttrFromCot(converter, s)
	if err != nil {
		return nil, 0, err
	}
	if attr == "" {
		if converter.curField.Value != "" { 
			attr = converter.curField.Value
		} else {
			attr = "0"
		}
	}
	i, err := strconv.ParseInt(attr, 10, 32)
	if err != nil {
		return nil, 0, err
	}
	switch converter.curField.Length {
	case 1:

		*converter.buffer = append(*converter.buffer, byte(i))
	case 2:

		*converter.buffer = binary.BigEndian.AppendUint16(*converter.buffer, uint16(i))
	case 4:
		*converter.buffer = binary.BigEndian.AppendUint32(*converter.buffer, uint32(i))
	}
	return converter.buffer, converter.curField.Length, nil
}

type ColorConverter struct{}

func (c *ColorConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	bytes := (*converter.buffer)[converter.offset : converter.offset+converter.curField.Length]
	
	u := int32(binary.BigEndian.Uint32(bytes))
	
	color := strconv.Itoa(int(u))
	v := int32(binary.BigEndian.Uint32([]byte{0xFF, bytes[1], bytes[2], bytes[3]}))
	strokeColor := strconv.Itoa(int(v))
	node, _, err := getCotParentNodeFromCot(converter, true, s)
	if err != nil {
		return nil, 0, err
	}
	node.AddChild("color", map[string]string{"value": color}, "")
	node.AddChild("strokeColor", map[string]string{"value": strokeColor}, "")
	node.AddChild("strokeWidth", map[string]string{"value": "4.0"}, "")
	node.AddChild("fillColor", map[string]string{"value": color}, "")
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}
func (c *ColorConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	attr, err := getAttrFromCot(converter, s)
	if err != nil {
		return nil, 0, err
	}
	color, err := strconv.Atoi(attr)
	if err != nil {
		return nil, 0, err
	}
	*converter.buffer = binary.BigEndian.AppendUint32(*converter.buffer, uint32(color))
	return nil, 0, nil
}


type SubNetTypeConverter struct{}

func (c *SubNetTypeConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}
func (c *SubNetTypeConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	*converter.buffer = append(*converter.buffer, 0x00)
	return converter.buffer, converter.curField.Length, nil
}

type MsgTypeConverter struct{}

func (c *MsgTypeConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	converter.cotEvent.Type = converter.msg.Content[0].Type
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}
func (c *MsgTypeConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	*converter.buffer = append(*converter.buffer, msgTypeToId[converter.msg.Content[0].Type])
	return converter.buffer, converter.curField.Length, nil
}



type RoleGroupConverter struct{}

func (c *RoleGroupConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	converter.cotEvent.Type = converter.msg.Content[0].Type
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}
func (c *RoleGroupConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	*converter.buffer = append(*converter.buffer, 0x00)
	return converter.buffer, converter.curField.Length, nil
}


type LengthLimitedFloatConverter struct{}

func (c *LengthLimitedFloatConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	
	value := MapBinaryToFloat(converter.buffer, converter.curField, converter.offset)
	
	val := strconv.FormatFloat(value, 'g', -1, 64)
	
	_, err := insertAttrToCot(converter, val, s)
	if err != nil {
		return converter.cotEvent, converter.offset, err
	}
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}
func (c *LengthLimitedFloatConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	
	val, err := getAttrFromCot(converter, s)
	if val == "" && converter.curField.Value != "" {
		val = converter.curField.Value
	} else {
		val = "0.0"
	}
	if err != nil {
		return nil, -1, err
	}
	value, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return nil, -1, err
	}
	
	
	field := converter.curField
	buf := MapFloatToBinary(value, field)
	*converter.buffer = append(*converter.buffer, buf...)
	return converter.buffer, field.Length, nil
}


type PointLengthLimitedFloatConverter struct{}

func (c *PointLengthLimitedFloatConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	value := MapBinaryToFloat(converter.buffer, converter.curField, converter.offset)
	eventElem := reflect.ValueOf(converter.cotEvent).Elem()
	pointField := eventElem.FieldByName("Point")
	
	field := strings.Split(converter.curField.Name, ".")[1]
	field = CapitalUpperCase(field)
	
	
	targetField := pointField.FieldByName(field)
	if !targetField.CanSet() {
		return nil, -1, fmt.Errorf("error: the target field [%s] cannot be set", field)
	}
	targetField.SetFloat(value)
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}
func (c *PointLengthLimitedFloatConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	point := converter.cotEvent.Point
	
	pv := reflect.ValueOf(point)
	
	field := strings.Split(converter.curField.Name, ".")[1]
	if 'a' <= field[0] && field[0] <= 'z' {
		field = string(field[0]+'A'-'a') + field[1:]
	}
	
	targetField := pv.FieldByName(field)
	if !targetField.IsValid() {
		return nil, 0, fmt.Errorf("error: the target field [%s] is not exist", field)
	}
	val := targetField.Float()
	buf := MapFloatToBinary(val, converter.curField)
	*converter.buffer = append(*converter.buffer, buf...)
	return converter.buffer, converter.curField.Length, nil
}

type RemarksCodecConverter struct{}

func (c *RemarksCodecConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	buf := (*converter.buffer)[converter.offset:]
	content := base64.StdEncoding.EncodeToString(buf)
	_, err := insertAttrToCot(converter, content, s)
	if err != nil {
		return nil, 0, err
	}
	return converter.cotEvent, converter.offset + len(buf), nil
}

func (c *RemarksCodecConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	attr, err := getAttrFromCot(converter, s)
	if err != nil {
		return nil, -1, err
	}
	codec, err := base64.StdEncoding.DecodeString(attr)
	if err != nil {
		return nil, -1, err
	}
	*converter.buffer = append(*converter.buffer, codec...)
	return converter.buffer, len(codec), nil
}


type UidGroupCodecConverter struct{}

func (c *UidGroupCodecConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	buf := *converter.buffer
	hash := fnv.New32a()
	_, _ = hash.Write(buf)
	
	uid := hex.EncodeToString(hash.Sum(nil)) + "@" + strconv.Itoa(int(buf[converter.offset]))
	converter.cotEvent.UID = uid
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}
func (c *UidGroupCodecConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	uid := converter.cotEvent.UID
	splitN := strings.SplitN(uid, "@", 2)
	gid := 0
	if len(splitN) == 2 {
		_gid, err := strconv.Atoi(splitN[1])
		if err == nil {
			gid = _gid
		}
	}
	*converter.buffer = append(*converter.buffer, uint8(gid))
	return converter.buffer, 0, nil
}

type UidConverter struct{}

func (c *UidConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	buf := *converter.buffer
	uid := hex.EncodeToString(buf[converter.offset : converter.offset+converter.curField.Length])
	converter.cotEvent.UID = uid
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}
func (c *UidConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	attr := converter.cotEvent.UID
	uid, err := strconv.ParseInt(attr, 16, 64)
	if err != nil {
		hash := fnv.New32a()
		_, _ = hash.Write([]byte(attr))
		uid = int64(hash.Sum32())
	}
	*converter.buffer = binary.BigEndian.AppendUint16(*converter.buffer, uint16(uid))
	return converter.buffer, 0, nil
}

type ChatUidConverter struct{}

func (c ChatUidConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	field, i, err := fieldConverters["uidConverter"].toEventField(s, converter)
	if err != nil {
		return nil, -1, err
	}
	name := converter.curField.Name
	converter.curField.Name = "detail/__chat.senderCallsign"
	callsign, err := getAttrFromCot(converter, s)
	if err != nil {
		return nil, -1, err
	}
	converter.cotEvent.UID = fmt.Sprintf("GeoChat.%s.All Chat Rooms.%s", callsign, uuid.New().String())
	converter.curField.Name = name
	return field, i, err
}

func (c ChatUidConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	return fieldConverters["uidConverter"].toBinaryField(s, converter)
}

type StringReflectConverter struct{}

func (c *StringReflectConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	text := converter.curField.Value
	lenText := 0
	if text == "" {
		lSize := 0
		buf := *converter.buffer
		
		switch converter.curField.SizeLimit {
		case 0x7F:
			lSize = 1
			lenText = int(buf[converter.offset])
		case 0x7FFF:
			lSize = 2
			lenText = int(binary.BigEndian.Uint16(buf[converter.offset : converter.offset+lSize]))
		default:
			return nil, -1, fmt.Errorf("error: the size is not a valid value, please use 127 (0x7F) or 32767 (0x7FFF)")
		}
		
		converter.offset += lSize
		
		text = string(buf[converter.offset : converter.offset+lenText])
	}
	if strings.HasPrefix(converter.curField.Name, ".") { 
		elem := reflect.ValueOf(converter.cotEvent).Elem()
		
		field := strings.TrimPrefix(converter.curField.Name, ".")
		
		field = CapitalUpperCase(field)
		targetField := elem.FieldByName(field)
		if !targetField.CanSet() {
			return nil, -1, fmt.Errorf("error: the target field [%s] cannot be set", field)
		}
		targetField.SetString(text)
	} else {
		return converter.cotEvent, -1, fmt.Errorf("error: reflect should be start with dot(.), got %s", converter.curField.Name)
	}
	return converter.cotEvent, converter.offset + lenText, nil
}

func (c *StringReflectConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	
	if converter.curField.Value != "" {
		return converter.buffer, 0, nil
	}
	if !strings.HasPrefix(converter.curField.Name, ".") { 
		return nil, 0, fmt.Errorf("error: reflect should be start with dot(.), got %s", converter.curField.Name)
	}
	elem := reflect.ValueOf(converter.cotEvent).Elem()
	
	field := strings.TrimPrefix(converter.curField.Name, ".")
	
	field = CapitalUpperCase(field)
	targetField := elem.FieldByName(field)
	
	text := targetField.String()
	lenText := len(text)
	buf := *converter.buffer
	
	deltaSize := 0
	switch converter.curField.SizeLimit {
	case 0x7F:
		deltaSize = 1
		buf = append(buf, uint8(lenText))
	case 0x7FFF:
		deltaSize = 2
		buf = binary.BigEndian.AppendUint16(buf, uint16(lenText))
	}
	deltaSize += lenText
	buf = append(buf, text...)
	*converter.buffer = buf
	return converter.buffer, deltaSize, nil
}

type StringConverter struct{}

func (c *StringConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	lSize := 0
	lenText := 0
	buf := *converter.buffer
	
	switch converter.curField.SizeLimit {
	case 0x7F:
		lSize = 1
		lenText = int(buf[converter.offset])
	case 0x7FFF:
		lSize = 2
		lenText = int(binary.BigEndian.Uint16(buf[converter.offset : converter.offset+lSize]))
	default:
		return nil, -1, fmt.Errorf("error: the size is not a valid value, please use 127 (0x7F) or 32767 (0x7FFF)")
	}
	
	converter.offset += lSize
	
	text := string(buf[converter.offset : converter.offset+lenText])
	_, err := insertAttrToCot(converter, text, s)
	if err != nil {
		return nil, -1, err
	}
	return converter.cotEvent, converter.offset + lenText, nil
}
func (c *StringConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	text, err := getAttrFromCot(converter, s)
	if err != nil {
		return nil, 0, err
	}
	var buf []byte
	lenText := len(text)
	
	if lenText > converter.curField.SizeLimit {
		return nil, 0, fmt.Errorf("error: the text length exceeds the limit of current field, limit=%d, actual size=%d", converter.curField.SizeLimit, lenText)
	}
	
	deltaSize := 0
	switch converter.curField.SizeLimit {
	case 0x7F:
		deltaSize = 1
		buf = append(buf, uint8(lenText))
	case 0x7FFF:
		deltaSize = 2
		buf = binary.BigEndian.AppendUint16(buf, uint16(lenText))
	}
	deltaSize += lenText
	buf = append(buf, text...)
	*converter.buffer = append(*converter.buffer, buf...)
	return converter.buffer, deltaSize, nil
}

type LinkPointConverter struct{}

func (c *LinkPointConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	
	if s.IsFirst() {
		parentNode, _, err := getCotParentNodeFromCot(converter, true, s)
		if err != nil {
			return nil, 0, err
		}
		s.SetParentNode(parentNode)
	}
	
	pointFields := Refs["ref-point"].Content
	offset := converter.offset
	var val []byte
	
	for _, field := range pointFields {
		
		f := MapBinaryToFloat(converter.buffer, &field, offset)
		val = append(val, strconv.FormatFloat(f, 'g', -1, 64)...)
		val = append(val, ',')
		offset += field.Length
	}
	
	value := string(val[:len(val)-1])
	paths := strings.Split(converter.curField.Name, "/")
	nameAttr := strings.Split(paths[len(paths)-1], ".")
	s.parentNode.AddChild(nameAttr[0], map[string]string{nameAttr[1]: value}, "")
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}
func (c *LinkPointConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	nameAttr := strings.Split(converter.curField.Name, ".")
	attrs := strings.Split(s.GetCurrentNodeByName(nameAttr[0]).GetAttr(nameAttr[1]), ",")
	
	for idx, field := range Refs["ref-point"].Content {
		
		item := "0.0"
		if idx < len(attrs) && attrs[idx] != "" { 
			item = attrs[idx]
		}
		f, err := strconv.ParseFloat(item, 64)
		if err != nil {
			return nil, 0, err
		}
		buf := MapFloatToBinary(f, &field)
		*converter.buffer = append(*converter.buffer, buf...)
	}
	
	return converter.buffer, converter.curField.Length, nil
}

type ObstaclesConverter struct{}

func (c ObstaclesConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	curField := converter.curField
	buf := bytes.Buffer{}
	for _, field := range Refs["ref-terrain"].Content {
		converter.curField = &field
		attr, err := getAttrFromCot(converter, s)
		if err != nil {
			return nil, 0, err
		}
		if attr != "true" {
			continue
		}
		switch field.Name {
		case "detail/_medevac_.terrain_none":
			buf.WriteString("None\n")
		case "detail/_medevac_.terrain_slope":
			{
				buf.WriteString("Sloping terrain to the ")
				n := converter.curField.Name
				converter.curField.Name = "detail/_medevac_.terrain_slope_dir"
				attr, err := getAttrFromCot(converter, s)
				converter.curField.Name = n
				if err != nil {
					return nil, 0, err
				}
				buf.WriteString(attr)
				buf.WriteRune('\n')
			}
		case "detail/_medevac_.terrain_rough":
			buf.WriteString("Rough terrain\n")
		case "detail/_medevac_.terrain_loose":
			buf.WriteString("Loose sand/dirt\n")
		case "detail/_medevac_.terrain_other":
			buf.WriteString("Other (Specify)\n")
		}
	}
	converter.curField = curField
	buf.Truncate(buf.Len() - 1)
	_, err := insertAttrToCot(converter, buf.String(), s)
	if err != nil {
		return nil, 0, err
	}
	return converter.cotEvent, converter.offset + converter.curField.Length, nil
}

func (c ObstaclesConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	return converter.buffer, 0, nil
}

type RouteLinkPointConverter struct{}

func (r RouteLinkPointConverter) toEventField(s *ArrayStatus, converter *Converter) (*cot.Event, int, error) {
	event, offset, err := fieldConverters["linkPointConverter"].toEventField(s, converter)
	if err != nil {
		return nil, 0, err
	}
	all := s.GetParentNode().GetAll("link")
	attrs := all[len(all)-1].Attrs
	attrs = append(attrs,
		xml.Attr{Name: xml.Name{Local: "uid"}, Value: uuid.New().String()},
		xml.Attr{Name: xml.Name{Local: "remarks"}, Value: ""},
		xml.Attr{Name: xml.Name{Local: "relation"}, Value: "c"})
	if s.IsFirst() || s.IsLast() {
		
		attrs = append(attrs, xml.Attr{
			Name:  xml.Name{Local: "type"},
			Value: "b-m-p-w",
		})
		
		if s.IsFirst() {
			name := converter.curField.Name
			converter.curField.Name = "detail/contact.callsign"
			callsign, err := getAttrFromCot(converter, &ArrayStatus{index: 0, size: 1})
			if err != nil {
				callsign = "CP"
			}
			attrs = append(attrs, xml.Attr{
				Name:  xml.Name{Local: "callsign"},
				Value: callsign,
			})
			converter.curField.Name = name
		}
		
		if s.IsLast() {
			attrs = append(attrs, xml.Attr{
				Name:  xml.Name{Local: "callsign"},
				Value: "EP",
			})
		}
	} else {
		
		attrs = append(attrs, xml.Attr{
			Name:  xml.Name{Local: "type"},
			Value: "b-m-p-c",
		}, xml.Attr{
			Name:  xml.Name{Local: "callsign"},
			Value: "",
		})
	}
	all[len(all)-1].Attrs = attrs
	return event, offset, nil
}

func (r RouteLinkPointConverter) toBinaryField(s *ArrayStatus, converter *Converter) (*[]byte, int, error) {
	return fieldConverters["linkPointConverter"].toBinaryField(s, converter)
}
