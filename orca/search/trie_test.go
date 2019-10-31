package trie

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

func genAllWords(lexems *[]string, words *[]string, word string, depth int) {
	if depth == 0 {
		*words = append(*words, word)
		return
	}
	for i, cl := range *lexems {
		if cl == "" {
			continue
		}
		(*lexems)[i] = ""
		genAllWords(lexems, words, word+cl, depth-1)
		(*lexems)[i] = cl
	}
}

func GenAllWords(lexems *[]string) *[]string {
	words := []string{}
	for i := 0; i < len(*lexems); i++ {
		genAllWords(lexems, &words, "", i)
	}

	return &words
}
func TestTrie(t *testing.T) {
	lexems := []string{
		"a",
		"b",
		"c",
		"d",
		"e",
		"f",
		"g",
		"h",
	}
	wordsIn := *GenAllWords(&lexems)
	trie := New()
	for _, word := range wordsIn {
		// fmt.Println(word)
		trie.Add(word)
	}

	wordsOut := *(trie.DumpData())

	sort.Strings(wordsIn)
	sort.Strings(wordsOut)

	if len(wordsIn) != len(wordsOut) {
		t.Fatal("Lists are different length!")
	}

	for i, v := range wordsIn {
		if v != wordsOut[i] {
			t.Fatal("Lists are different!", i, wordsIn[i], wordsOut[i])
		}
	}


	// Delete half of the words randomly

	tgtLen := len(wordsIn) / 2
	// tgtLen = 100
	for len(wordsIn) > tgtLen {
		n := rand.Intn(len(wordsIn))
		if !trie.Remove(wordsIn[n]) {
			t.Fatal("Not found!")
		}
		wordsIn = append(wordsIn[:n], wordsIn[n+1:]...)
	}
	wordsOut = *trie.DumpData()
	sort.Strings(wordsOut)
	fmt.Println(len(wordsIn), len(wordsOut))
	// if len(wordsIn) != len(wordsOut) {
	// 	t.Fatal("Lists are different length!")
	// }
	// trie.Print()
	for i, v := range wordsIn {
		if v != wordsOut[i] {
			t.Fatal("Lists are different!", i, wordsIn[i], wordsOut[i])
		}
	}
	// trie.Print()
}
