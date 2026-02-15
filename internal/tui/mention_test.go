package tui

import "testing"

func TestParseMention_SingleMessage(t *testing.T) {
	t.Run("basic @agent message", func(t *testing.T) {
		r := ParseMention("@coder fix this bug")
		if r.AgentID != "coder" || r.Message != "fix this bug" || r.Sticky != false {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want AgentID=coder, Message=fix this bug, Sticky=false", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_StickyDoubleAt(t *testing.T) {
	t.Run("sticky @@ with no message", func(t *testing.T) {
		r := ParseMention("@@coder")
		if r.AgentID != "coder" || r.Message != "" || r.Sticky != true {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want AgentID=coder, Message=, Sticky=true", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_StickyDoubleAtWithMessage(t *testing.T) {
	t.Run("sticky @@ with message", func(t *testing.T) {
		r := ParseMention("@@researcher find papers on RAG")
		if r.AgentID != "researcher" || r.Message != "find papers on RAG" || r.Sticky != true {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want AgentID=researcher, Message=find papers on RAG, Sticky=true", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_BareAgentIsSticky(t *testing.T) {
	t.Run("bare @agent with no message becomes sticky", func(t *testing.T) {
		r := ParseMention("@coder")
		if r.AgentID != "coder" || r.Message != "" || r.Sticky != true {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want AgentID=coder, Message=, Sticky=true", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_PlainMessage(t *testing.T) {
	t.Run("plain message without mention", func(t *testing.T) {
		r := ParseMention("hello world")
		if r.AgentID != "" || r.Message != "hello world" || r.Sticky != false {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want AgentID=, Message=hello world, Sticky=false", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_EmptyInput(t *testing.T) {
	t.Run("empty input string", func(t *testing.T) {
		r := ParseMention("")
		if r.AgentID != "" || r.Message != "" || r.Sticky != false {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want all empty/false", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_WhitespaceOnly(t *testing.T) {
	t.Run("whitespace only input", func(t *testing.T) {
		r := ParseMention("   ")
		if r.AgentID != "" || r.Message != "" || r.Sticky != false {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want all empty/false", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_SlashCommand(t *testing.T) {
	t.Run("slash command not treated as mention", func(t *testing.T) {
		r := ParseMention("/help")
		if r.AgentID != "" || r.Message != "/help" || r.Sticky != false {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want AgentID=, Message=/help, Sticky=false", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_SlashAgentCommand(t *testing.T) {
	t.Run("slash agent command not treated as mention", func(t *testing.T) {
		r := ParseMention("/agent switch coder")
		if r.AgentID != "" || r.Message != "/agent switch coder" || r.Sticky != false {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want AgentID=, Message=/agent switch coder, Sticky=false", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_HyphenatedAgentID(t *testing.T) {
	t.Run("hyphenated agent ID valid", func(t *testing.T) {
		r := ParseMention("@code-reviewer check PR")
		if r.AgentID != "code-reviewer" || r.Message != "check PR" || r.Sticky != false {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want AgentID=code-reviewer, Message=check PR, Sticky=false", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_InvalidChars(t *testing.T) {
	t.Run("invalid chars in agent ID", func(t *testing.T) {
		r := ParseMention("@agent! foo")
		if r.AgentID != "" || r.Message != "@agent! foo" || r.Sticky != false {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want all invalid/unchanged", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_BareAt(t *testing.T) {
	t.Run("bare @ sign only", func(t *testing.T) {
		r := ParseMention("@")
		if r.AgentID != "" || r.Message != "@" || r.Sticky != false {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want AgentID=, Message=@, Sticky=false", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_BareDoubleAt(t *testing.T) {
	t.Run("bare @@ sign only", func(t *testing.T) {
		r := ParseMention("@@")
		if r.AgentID != "" || r.Message != "@@" || r.Sticky != false {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want AgentID=, Message=@@, Sticky=false", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_AtWithSpace(t *testing.T) {
	t.Run("@ followed by space", func(t *testing.T) {
		r := ParseMention("@ hello")
		if r.AgentID != "" || r.Message != "@ hello" || r.Sticky != false {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want AgentID=, Message=@ hello, Sticky=false", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_TrailingHyphen(t *testing.T) {
	t.Run("trailing hyphen invalid", func(t *testing.T) {
		r := ParseMention("@coder- fix this")
		if r.AgentID != "" || r.Message != "@coder- fix this" || r.Sticky != false {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want all invalid/unchanged", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_LeadingWhitespace(t *testing.T) {
	t.Run("leading whitespace trimmed", func(t *testing.T) {
		r := ParseMention("  @coder fix")
		if r.AgentID != "coder" || r.Message != "fix" || r.Sticky != false {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want AgentID=coder, Message=fix, Sticky=false", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_UppercaseAgentID(t *testing.T) {
	t.Run("uppercase in agent ID", func(t *testing.T) {
		r := ParseMention("@Coder fix this")
		if r.AgentID != "Coder" || r.Message != "fix this" || r.Sticky != false {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want AgentID=Coder, Message=fix this, Sticky=false", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_NumericAgentID(t *testing.T) {
	t.Run("numeric characters in agent ID", func(t *testing.T) {
		r := ParseMention("@agent1 do stuff")
		if r.AgentID != "agent1" || r.Message != "do stuff" || r.Sticky != false {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want AgentID=agent1, Message=do stuff, Sticky=false", r.AgentID, r.Message, r.Sticky)
		}
	})
}

func TestParseMention_LongMessage(t *testing.T) {
	t.Run("long message content", func(t *testing.T) {
		r := ParseMention("@writer write a blog post about distributed systems and why they fail")
		if r.AgentID != "writer" || r.Message != "write a blog post about distributed systems and why they fail" || r.Sticky != false {
			t.Errorf("got AgentID=%q, Message=%q, Sticky=%v; want AgentID=writer, Message=write a blog post..., Sticky=false", r.AgentID, r.Message, r.Sticky)
		}
	})
}
