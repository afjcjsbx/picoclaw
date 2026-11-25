package shellguard

import "strings"

// envArgs is the statically parsed surface of an env invocation.
type envArgs struct {
	command      []word
	assignments  []word
	chdir        word
	hasChdir     bool
	unset        []string
	unsetDynamic bool
	ignoreEnv    bool
	splitString  bool
	splitValue   word
	dynamic      bool
}

// parseEnvArgs parses env options once. It knows which options consume a value,
// so -u NAME -S "cmd" and -C DIR -S "cmd" still expose the split string.
func parseEnvArgs(args []word) envArgs {
	var out envArgs
	i := 0

options:
	for i < len(args) {
		w := args[i]
		text := w.text
		stop := false
		switch {
		case isAssignment(text):
			out.assignments = append(out.assignments, w)
			i++
		case w.dynamic:
			out.dynamic = true
			return out
		case text == "--":
			i++
			break options
		case text == "-":
			out.ignoreEnv = true
			i++
		case !strings.HasPrefix(text, "-"):
			break options
		case strings.HasPrefix(text, "--"):
			i, stop = out.longOption(args, i)
		default:
			i, stop = out.shortOptions(args, i)
		}
		if stop {
			return out
		}
	}

	for i < len(args) && !args[i].dynamic && isAssignment(args[i].text) {
		out.assignments = append(out.assignments, args[i])
		i++
	}
	if i < len(args) {
		out.command = args[i:]
	}
	return out
}

// longOption parses the --option at args[i]. It returns the index of the next
// word and whether parsing is over: a split string swallows the rest, and an
// option missing its value makes the command unresolvable.
func (o *envArgs) longOption(args []word, i int) (next int, done bool) {
	text := args[i].text
	switch {
	case text == "--ignore-environment":
		o.ignoreEnv = true
	case text == "--unset":
		if i+1 >= len(args) {
			o.dynamic = true
			return i, true
		}
		i++
		o.addUnset(args[i])
	case strings.HasPrefix(text, "--unset="):
		o.unset = append(o.unset, strings.TrimPrefix(text, "--unset="))
	case text == "--chdir":
		if i+1 >= len(args) {
			o.dynamic = true
			return i, true
		}
		i++
		o.chdir, o.hasChdir = args[i], true
	case strings.HasPrefix(text, "--chdir="):
		o.chdir, o.hasChdir = word{text: strings.TrimPrefix(text, "--chdir=")}, true
	case text == "--split-string":
		o.splitString = true
		if i+1 < len(args) {
			o.splitValue = args[i+1]
		}
		return i, true
	case strings.HasPrefix(text, "--split-string="):
		o.splitString = true
		o.splitValue = word{text: strings.TrimPrefix(text, "--split-string=")}
		return i, true
	}
	// --null and unknown long options take no value.
	return i + 1, false
}

// shortOptions parses the clustered -iu... options at args[i], with the same
// result as longOption.
func (o *envArgs) shortOptions(args []word, i int) (next int, done bool) {
	text := args[i].text
	for j := 1; j < len(text); j++ {
		switch text[j] {
		case 'i':
			o.ignoreEnv = true
		case 'u', 'C':
			value, hasValue := optionValue(text, j+1, args, i)
			if !hasValue {
				o.dynamic = true
				return i, true
			}
			if text[j] == 'u' {
				o.addUnset(value.word)
			} else {
				o.chdir, o.hasChdir = value.word, true
			}
			if value.next {
				i++
			}
			j = len(text) - 1
		case 'S':
			o.splitString = true
			if value, hasValue := optionValue(text, j+1, args, i); hasValue {
				o.splitValue = value.word
			}
			return i, true
		}
	}
	return i + 1, false
}

func (o *envArgs) addUnset(name word) {
	if name.dynamic {
		o.unsetDynamic = true
		return
	}
	o.unset = append(o.unset, name.text)
}

type envOptionValue struct {
	word word
	next bool
}

// optionValue returns the value of a short env option. The rest of the option
// word is an attached value; otherwise the next word is the value.
func optionValue(text string, rest int, args []word, index int) (envOptionValue, bool) {
	if rest < len(text) {
		return envOptionValue{word: word{text: text[rest:]}}, true
	}
	if index+1 >= len(args) {
		return envOptionValue{}, false
	}
	return envOptionValue{word: args[index+1], next: true}, true
}
