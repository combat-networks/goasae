package message

import (
	"bytes"
	"embed"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"github.com/kdudkov/goasae/pkg/cot"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"
)

var Rt Root
var msgTypeToId = make(map[string]byte)
var msgTypeMatchFuncFromEvent = make(map[string]func(cotEvent *cot.Event) bool)

const (
	ErrMsgTooShort     = -1
	ErrMsgTypeNotExist = -2
	ErrUnknownMsgLen   = -3
)


var EFS embed.FS

var IconPathMap = NewPathMap[*IconPathTable]()
var IconFileMap = NewPathMap[*IconFileTable]()
var TypeIconMap = NewPathMap[*TypeIconTable]()

func initConverterByConfig() {
	jsonBytes, err := EFS.ReadFile("config/msgConverterConf.json")//Linux下使用绝对路径
	if err != nil {
		fmt.Println("error reading config:", err)
		os.Exit(-1)
	}
	err = json.Unmarshal(jsonBytes, &Rt)
	if err != nil {
		fmt.Println("Error parsing JSON:", err)
		os.Exit(-1)
	}
	
	
	for _, ref := range Rt.Refs {
		Refs[ref.Name] = ref
	}
	
	for index, message := range Rt.Messages {
		msgTypeToId[message.Content[0].Type] = byte(index)
	}
	
	msgTypeMatchFuncFromEvent["2512icon"] = func(event *cot.Event) bool {
		if usericon := event.Detail.GetFirst("usericon"); usericon != nil {
			attr := usericon.GetAttr("iconsetpath")
			return strings.HasPrefix(attr, "COT_MAPPING_2525C")
		}
		return false
	}
	msgTypeMatchFuncFromEvent["simpleIcon"] = func(event *cot.Event) bool {
		if usericon := event.Detail.GetFirst("usericon"); usericon != nil {
			if icp := strings.Split(usericon.GetAttr("iconsetpath"), "/"); len(icp) == 3 {
				_, ok := IconFileMap.GetValue(icp[2])
				return ok
			}
		}
		return false
	}
}

func InitTypeIconTableByConfig() {
	id := 0
	csvbytes, err := EFS.ReadFile("config/typeIconTable.csv")//Linux下使用绝对路径
	if err != nil {
		log.Fatal(err)
	}
	reader := csv.NewReader(bytes.NewBuffer(csvbytes))
	TypeIconMap = NewPathMap[*TypeIconTable]()
	for record, err := reader.Read(); err == nil; record, err = reader.Read() {
		err := TypeIconMap.Add(&TypeIconTable{
			Type: record[0],
			Name: record[1],
			Id:   id,
		})
		if err != nil {
			log.Fatal(err)
		}
		id++
	}
}

func initIconFileTableByConfig() {
	id := 0
	csvbytes, err := EFS.ReadFile("config/iconFileTable.csv")
	if err != nil {
		log.Fatal(err)
	}
	reader := csv.NewReader(bytes.NewBuffer(csvbytes))
	IconFileMap = NewPathMap[*IconFileTable]()
	for record, err := reader.Read(); err == nil; record, err = reader.Read() {
		err := IconFileMap.Add(&IconFileTable{
			Name: record[0],
			Id:   id,
		})
		if err != nil {
			log.Fatal(err)
		}
		id++
	}
}

func initIconPathTableByConfig() {
	id := 0
	csvbytes, err := EFS.ReadFile("config/iconPathTable.csv")
	if err != nil {
		log.Fatal(err)
	}
	reader := csv.NewReader(bytes.NewBuffer(csvbytes))
	IconPathMap = NewPathMap[*IconPathTable]()
	for record, err := reader.Read(); err == nil; record, err = reader.Read() {
		err := IconPathMap.Add(&IconPathTable{
			Category:  record[0],
			GroupName: record[1],
			Uuid:      record[2],
			Id:        id,
		})
		if err != nil {
			log.Fatal(err)
		}
		id++
	}
}

func InitIconTablesByConfig() {
	initIconPathTableByConfig()
	initIconFileTableByConfig()
	InitTypeIconTableByConfig()
}

func init() {
//	initConverterByConfig()
//	InitIconTablesByConfig()
}


type Converter struct {
	msg            *Message
	cotEvent       *cot.Event
	curField       *Field
	buffer         *[]byte
	offset         int                    
	estimateLength int                    
	storage        map[string]interface{} 
}


func NewConverterFromEvent(event *cot.Event) (*Converter, error) {
	var msg *Message
	for _, message := range Rt.Messages {
		typ := message.Content[0].Type
		typeMatch := message.Content[0].TypeMatch
		switch typeMatch {
		case "prefix":
			if strings.HasPrefix(typ, event.Type) || strings.HasPrefix(event.Type, typ) {
				msg = message
				goto matchSuccess
			}
		case "suffix":
			if strings.HasSuffix(typ, event.Type) || strings.HasSuffix(event.Type, typ) {
				msg = message
				goto matchSuccess
			}
		case "", "all":
			if typ == event.Type {
				msg = message
				goto matchSuccess
			}
		default:
			isMatch, ok := msgTypeMatchFuncFromEvent[typeMatch]
			if !ok {
				return nil, fmt.Errorf("cannot found a type match function for rule [%s]", typeMatch)
			}
			if isMatch(event) {
				msg = message
				goto matchSuccess
			}
		}
	}
	
	return nil, fmt.Errorf("cannot create a converter for unknown type %s", event.Type)
	
matchSuccess:
	return &Converter{
		msg:      msg,
		cotEvent: event,
		curField: nil,
		buffer:   &[]byte{},
		offset:   0,
	}, nil
}

func GetMessageExpectedLength(data []byte) (int, error) {
	if len(data) < 2 {
		return ErrMsgTooShort, fmt.Errorf("message is too short to determine the type") 
	}
	if int(data[1]) > len(Rt.Messages) {
		return ErrMsgTypeNotExist, fmt.Errorf("no message converter found for typeid %d", data[1]) 
	}
	msg := Rt.Messages[data[1]]
	if msg == nil {
		return ErrMsgTypeNotExist, fmt.Errorf("no message converter found for typeid %d", data[1]) 
	}
	if strings.HasPrefix(msg.Content[0].Name, "ref-head") { 
		
		for _, field := range Refs["ref-tail"].Content {
			if len(data) < field.Offset+field.Length {
				return ErrMsgTooShort, fmt.Errorf("message is too short to determine the length")
			}
			if field.Name == "messageLength" {
				dat := data[field.Offset : field.Offset+field.Length]
				return int(binary.BigEndian.Uint16(dat)), nil 
			}
		}
	}
	
	return len(data), nil
}


func NewConverterFromBinary(binary []byte) (*Converter, error) {
	if len(binary) < 2 {
		return nil, fmt.Errorf("the length of the binary is too short to determine the type")
	}
	idx := int(binary[1])
	if 0 <= idx && idx < len(Rt.Messages) {
		return &Converter{
			msg:      Rt.Messages[idx],
			cotEvent: cot.CotToEvent(cot.BasicMsg("", "", 24*time.Hour).CotEvent),
			curField: nil,
			buffer:   &binary,
			offset:   0,
		}, nil
	}
	return nil, fmt.Errorf("cannot create a converter for outbound typeid %d", idx)
}

func (c *Converter) convertFieldToEvent(field *Field, status *ArrayStatus) error {
	c.curField = field
	fc, ok := fieldConverters[field.Converter]
	if !ok {
		slog.Warn(fmt.Sprintf("field converter [%s] not found, using placeHolderConverter instead", field.Converter))
		fc = fieldConverters["placeHolderConverter"]
	}
	_, offset, err := fc.toEventField(status, c)
	
	if err != nil {
		return fmt.Errorf("convertion failed due to an error: %v", err)
	}
	
	c.offset = offset
	return nil
}

func (c *Converter) convertFieldToBinary(field *Field, status *ArrayStatus) error {
	c.curField = field
	fc, ok := fieldConverters[field.Converter]
	if !ok {
		slog.Warn(fmt.Sprintf("field converter [%s] not found, using placeHolderConverter instead", field.Converter))
		fc = fieldConverters["placeHolderConverter"]
	}
	_, _, err := fc.toBinaryField(status, c)
	if err != nil {
		return fmt.Errorf("convertion failed due to an error: %v", err)
	}
	return nil
}


func (c *Converter) convertFields(fields *[]Field, impl func(field *Field) error) error {
	for _, field := range *fields {
		err := impl(&field)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Converter) toEvent(fields *[]Field, s *ArrayStatus, ref *Ref) (*cot.Event, error) {
	if ref != nil && ref.Converter != "" {
		
		
		err := c.convertRefFieldArrToEvent(s, ref)
		if err != nil {
			return nil, err
		}
		return c.cotEvent, nil
	}
	
	for s.First(); s.IsInBounds(); s.Next() {
		
		for _, field := range *fields {
			name := field.Name
			if field.Type == "array" { 
				arrStat := ArrayStatus{parent: s}
				
				err := arrStat.InitFormBinary(c, &field)
				if err != nil {
					return nil, err
				}
				ref := Refs[name]
				refFields := ref.Content
				c.curField = &field
				if field.Converter == "" { 
					_, err = c.toEvent(&refFields, &arrStat, &ref)
				} else { 
					ref2 := ref
					ref2.Converter = field.Converter
					_, err = c.toEvent(&refFields, &arrStat, &ref2)
				}
				if err != nil {
					return nil, err
				}
			} else if strings.HasPrefix(name, "ref-") { 
				ref, ok := Refs[name]
				if !ok {
					return nil, fmt.Errorf("cannot find reference type [%s]", name)
				}
				refFields := ref.Content
				_, err := c.toEvent(&refFields, &ArrayStatus{
					index:  0,
					size:   1,
					parent: s,
				}, &ref)
				if err != nil {
					return nil, err
				}
			} else {
				
				err := c.convertFieldToEvent(&field, s)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	
	return c.cotEvent, nil
}

func (c *Converter) ToEvent() (*cot.Event, error) {
	if strings.HasPrefix(c.msg.Content[0].Name, "ref-head") { 
		tails, ok := Refs["ref-tail"]
		if ok {
			for _, field := range tails.Content {
				err := c.convertFieldToEvent(&field, &ArrayStatus{
					index: 0,
					size:  1,
				})
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return c.toEvent(&c.msg.Content, &ArrayStatus{
		index: 0,
		size:  1,
	}, nil)
}

func (c *Converter) toBinary(fields *[]Field, s *ArrayStatus, ref *Ref) ([]byte, error) {
	
	if ref != nil && ref.Converter != "" {
		err := c.convertRefFieldArrToBinary(s, ref)
		if err != nil {
			return nil, err
		}
		return *c.buffer, nil
	}
	for s.First(); s.IsInBounds(); s.Next() {
		for _, field := range *fields {
			name := field.Name
			if field.Type == "array" { 
				c.curField = &field
				arrStat := ArrayStatus{}
				
				err := arrStat.InitFormEvent(c, &field)
				if err != nil {
					return nil, err
				}
				
				if field.SizeLimit == 0x7F || field.SizeLimit == 0x7FFF {
					
					if arrStat.size <= 0x7F {
						*c.buffer = append(*c.buffer, uint8(arrStat.size))
					} else {
						return nil, fmt.Errorf("size limit exceeded 0x7F")
					}
				}
				ref := Refs[name]
				refFields := ref.Content
				_, err = c.toBinary(&refFields, &arrStat, &ref)
				if err != nil {
					return nil, err
				}
			} else if strings.HasPrefix(name, "ref-") { 
				ref := Refs[name]
				refFields := ref.Content
				_, err := c.toBinary(&refFields, &ArrayStatus{
					index: 0,
					size:  1,
				}, &ref)
				if err != nil {
					return nil, err
				}
			} else {
				err := c.convertFieldToBinary(&field, s)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return *c.buffer, nil
}

func (c *Converter) ToBinary() ([]byte, error) {
	_, err := c.toBinary(&c.msg.Content, &ArrayStatus{
		index: 0,
		size:  1,
	}, nil)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(c.msg.Content[0].Name, "ref-head") { 
		tails, ok := Refs["ref-tail"]
		if ok {
			for _, field := range tails.Content {
				err := c.convertFieldToBinary(&field, &ArrayStatus{
					index: 0,
					size:  1,
				})
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return *c.buffer, nil
}

func (c *Converter) convertRefFieldArrToEvent(s *ArrayStatus, ref *Ref) error {
	for s.First(); s.IsInBounds(); s.Next() {
		converter, ok := fieldConverters[ref.Converter]
		if !ok {
			return fmt.Errorf("field converter [%s] not found", ref.Converter)
		}
		_, offset, err := converter.toEventField(s, c)
		if err != nil {
			return err
		}
		c.offset = offset
	}
	return nil
}

func (c *Converter) convertRefFieldArrToBinary(s *ArrayStatus, ref *Ref) error {
	for s.First(); s.IsInBounds(); s.Next() {
		converter, ok := fieldConverters[ref.Converter]
		if !ok {
			return fmt.Errorf("cannot find converter [%s]", ref.Converter)
		}
		_, _, err := converter.toBinaryField(s, c)
		if err != nil {
			return err
		}
	}
	return nil
}
