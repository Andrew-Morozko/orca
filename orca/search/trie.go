package trie

import (
	"fmt"
	"strings"
)

type Trie struct {
	root   *Node
	length int
}
type Node struct {
	prefix         string
	parent         *Node
	children       map[byte]*Node
	isCompleteWord bool
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Gets first child from the children
func (n *Node) getChild() (child *Node) {
	for _, child = range n.children {
		break
	}
	return
}

func (n *Node) replaceChild(orig, replacement *Node) {
	n.children[orig.prefix[0]] = replacement
}

func (t *Trie) Search(str string) (guaranteedContinuation string, isExactMatch bool) {
	match, unmatched, partialMatchLen := t.search(str)
	if unmatched == "" {
		if partialMatchLen != 0 {
			guaranteedContinuation = match.prefix[partialMatchLen:]
			isExactMatch = len(match.children) == 0
		} else {
			if len(match.children) == 1 {
				ch := match.getChild()
				guaranteedContinuation = match.prefix + ch.prefix
				isExactMatch = len(ch.children) == 0
			} else {
				isExactMatch = len(match.children) == 0
			}
		}

	}
	return
}

func (t *Trie) search(str string) (match *Node, unmatched string, partialMatchLen int) {
	var node *Node
	match = t.root
	unmatched = str
	unmatchedLen := len(unmatched)
	var found bool
	for unmatchedLen != 0 {
		node, found = match.children[unmatched[0]]
		if !found {
			// Full match with leftovers, leftovers irredusible
			return
		}
		match = node
		prefixLen := len(match.prefix)
		if unmatchedLen >= prefixLen && match.prefix[1:] == unmatched[1:prefixLen] {
			// Full match with leftovers, continue loop
			unmatched = unmatched[prefixLen:]
			unmatchedLen -= prefixLen
		} else {
			// Partial match, end
			i := 1
			for i < min(prefixLen, unmatchedLen) && match.prefix[i] == unmatched[i] {
				i++
			}
			unmatched = unmatched[i:]
			partialMatchLen = i
			return
		}
	}
	return
}

func New() *Trie {
	return &Trie{
		root: &Node{
			children: make(map[byte]*Node),
		},
		length: 0,
	}
}

func (t *Trie) Add(str string) {
	match, unmatched, partialLen := t.search(str)
	if partialLen != 0 {
		// Need to split the node
		// Moving "match" to lower level
		newMatchNode := &Node{
			parent:         match,
			prefix:         match.prefix[partialLen:],
			children:       match.children,
			isCompleteWord: match.isCompleteWord,
		}
		for _, child := range match.children {
			child.parent = newMatchNode
		}
		match.children = map[byte]*Node{
			newMatchNode.prefix[0]: newMatchNode,
		}

		// Reconfiguring the other params
		match.prefix = match.prefix[:partialLen]
		match.isCompleteWord = false
		// unmatched = unmatched[partialLen:]
	}
	if unmatched == "" {
		if !match.isCompleteWord {
			t.length++
			match.isCompleteWord = true
		}
	} else {
		t.length++
		match.children[unmatched[0]] = &Node{
			parent:         match,
			prefix:         unmatched,
			isCompleteWord: true,
			children:       make(map[byte]*Node),
		}
	}
}

func (t *Trie) Remove(str string) (isFound bool) {
	defer func() {
		if isFound {
			t.length--
		}
	}()

	match, unmatched, partialLen := t.search(str)
	if unmatched != "" || partialLen != 0 {
		return false
	}
	if !match.isCompleteWord {
		return false
	}

	if match.parent == nil {
		// We're root, don't move anything
		match.isCompleteWord = false
		return true
	}
	switch len(match.children) {
	case 0:
		// We have no children - delete us from parent
		match.isCompleteWord = false
		delete(match.parent.children, match.prefix[0])

	case 1:
		// We have one child, replce self with the child
		var child, parent *Node
		parent = match.parent

		// Empty loop intended, allows to get child without knowing its prefix
		for _, child = range match.children {
			break
		}
		child.parent = parent
		child.prefix = match.prefix + child.prefix
		parent.children[child.prefix[0]] = child

	default:
		// We have many children, just remove isCompleteWord
		match.isCompleteWord = false
	}
	return true
}

func (t *Trie) Contains(str string) bool {
	match, unmatched, partialLen := t.search(str)
	return match.isCompleteWord && unmatched == "" && partialLen == 0
}

func (n *Node) printTree(level int) {
	fmt.Print(strings.Repeat(" ", level))
	if n.isCompleteWord {
		fmt.Print("\x1b[0;32m")
	}
	fmt.Print(n.prefix)
	prefLen := len(n.prefix)
	if n.isCompleteWord {
		fmt.Print("\x1b[0m")
	}
	fmt.Print("\n")
	for _, ch := range n.children {
		ch.printTree(level + prefLen)
	}
}
func (t *Trie) Print() {
	t.root.printTree(0)
}
func (n *Node) dumpData(prefix string, res *[]string) {
	if n.isCompleteWord {
		*res = append(*res, prefix)
	}
	for _, ch := range n.children {
		ch.dumpData(prefix+ch.prefix, res)
	}
}
func (t *Trie) DumpData() *[]string {
	res := make([]string, 0, t.length)
	t.root.dumpData("", &res)
	return &res
}
