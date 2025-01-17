package message

import (
	"github.com/kdudkov/goasae/pkg/cot"
)



type Filler func(c *Converter, curNode *cot.Node, nextNode string) (*cot.Node, error)

var fillerMap = make(map[string]Filler)

func init() {
	fillerMap["remarks"] = func(c *Converter, curNode *cot.Node, nextNode string) (*cot.Node, error) {

		name := c.curField.Name
		c.curField.Name = "detail/__chat.senderCallsign"
		callsign, err := getAttrFromCot(c, &ArrayStatus{index: 0, size: 1})
		if err != nil {
			c.curField.Name = "detail/contact.callsign"
			callsign, err = getAttrFromCot(c, &ArrayStatus{index: 0, size: 1})
			if err != nil {
				return nil, err
			}
		}
		c.curField.Name = name
		curNode.AddChild("link", map[string]string{"uid": callsign, "relation": "p-p", "type": c.msg.Content[0].Type}, "")
		curNode.AddChild("__serverdestination", map[string]string{"destinations": "0.0.0.0:4242:tcp:" + callsign}, "")
		
		result := curNode.AddChild("remarks", map[string]string{"time": GetCurrentFormatTime(), "source": "BAO.F.SAE." + callsign, "to": ALL_CHAT_ROOMS}, "")
		return result, nil
	}
	fillerMap["uid"] = func(c *Converter, curNode *cot.Node, nextNode string) (*cot.Node, error) {
		return curNode.AddChild("uid", map[string]string{}, ""), nil
	}
	fillerMap["contact"] = func(c *Converter, curNode *cot.Node, nextNode string) (*cot.Node, error) {
		return curNode.AddChild("contact", map[string]string{"endpoint": "*:-1:stcp"}, ""), nil
	}
	fillerMap["default"] = func(c *Converter, curNode *cot.Node, nextNode string) (*cot.Node, error) {
		return curNode.AddChild(nextNode, map[string]string{}, ""), nil
	}
}
