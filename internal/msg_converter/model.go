package message

var Refs = make(map[string]Ref)

type WithIdentifier interface {
	getId() int
	getKey() string
}

type IconFileTable struct {
	Name string
	Id   int
}

func (i *IconFileTable) getId() int {
	return i.Id
}

func (i *IconFileTable) getKey() string {
	return i.Name
}

type IconPathTable struct {
	GroupName string
	Id        int
	Category  string
	Uuid      string
}

func (i *IconPathTable) getId() int {
	return i.Id
}

func (i *IconPathTable) getKey() string {
	return i.Uuid + "/" + i.GroupName
}

type TypeIconTable struct {
	Name string
	Id   int
	Type string
}

func (i *TypeIconTable) getId() int {
	return i.Id
}
func (i *TypeIconTable) getKey() string {
	return i.Type
}

type Ref struct {
	Name      string  `json:"name"`
	Content   []Field `json:"content"`
	Path      string  `json:"path,omitempty"`
	Converter string  `json:"converter,omitempty"`
}

type Field struct {
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	TypeMatch      string   `json:"typeMatch,omitempty"` 
	Length         int      `json:"length"`
	RangeMin       float64  `json:"rangeMin,omitempty"` 
	RangeMax       float64  `json:"rangeMax,omitempty"` 
	Converter      string   `json:"converter"`          
	SizeLimit      int      `json:"sizeLimit,omitempty"`
	Offset         int      `json:"offset,default=-1"`        
	RelativeOffset int      `json:"relativeOffset,default=0"` 
	Selections     []string `json:"selections,omitempty"`     
	Value          string   `json:"value,omitempty"`
}

type Message struct {
	Content []Field `json:"content"`
}

type Root struct {
	Refs     []Ref      `json:"refs"`
	Messages []*Message `json:"messages"`
}
