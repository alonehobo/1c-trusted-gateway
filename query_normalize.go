package main

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// normalizeQueryAliases rewrites КАК/AS aliases in every statement of a 1C query
// batch so that each alias equals the expression it represents (dots, spaces,
// parentheses removed).  For batch queries (separated by ;) it tracks column
// renames introduced by earlier ПОМЕСТИТЬ statements and propagates them into
// field references of subsequent queries.
//
// This prevents an agent from mapping a sensitive field to a whitelisted alias.
func normalizeQueryAliases(query string) string {
	stmts := splitBySemicolon(query)
	if len(stmts) == 1 {
		return normalizeSingleQuery(stmts[0])
	}

	// tempRenames: tempTableName → {oldAlias → newAlias}
	tempRenames := make(map[string]map[string]string)

	for i, stmt := range stmts {
		// 1. Apply renames from previous temp tables to field references
		stmt = applyTempRenames(stmt, tempRenames)

		// 2. Collect alias mapping AFTER renames applied but BEFORE normalization
		aliasMap := collectSelectAliases(stmt)

		// 3. Normalize SELECT aliases in this statement
		stmt = normalizeSingleQuery(stmt)

		// 4. If this is a ПОМЕСТИТЬ, record renames for the temp table
		if tmpName := extractTempTableName(stmt); tmpName != "" && len(aliasMap) > 0 {
			tempRenames[strings.ToUpper(tmpName)] = aliasMap
		}

		stmts[i] = stmt
	}

	return strings.Join(stmts, ";")
}

// splitBySemicolon splits query batch by top-level semicolons.
func splitBySemicolon(query string) []string {
	parts := strings.Split(query, ";")
	if len(parts) > 0 && strings.TrimSpace(parts[len(parts)-1]) == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// collectSelectAliases extracts {oldAlias → canonicalAlias} from the SELECT clause.
// Skips КАК inside parentheses (e.g. ВЫРАЗИТЬ(X КАК Тип)).
func collectSelectAliases(stmt string) map[string]string {
	selectPart := extractSelectPart(stmt)
	runes := []rune(selectPart)
	n := len(runes)
	result := make(map[string]string)
	depth := 0

	i := 0
	for i < n {
		switch runes[i] {
		case '(':
			depth++
			i++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			i++
			continue
		}
		if depth > 0 {
			i++
			continue
		}

		kwLen, isKW := matchAliasKeyword(runes, i)
		if kwLen == 0 || !isKW {
			i++
			continue
		}
		if i > 0 && !isSpaceRune(runes[i-1]) {
			i++
			continue
		}

		kwEnd := i + kwLen
		j := kwEnd
		for j < n && isSpaceRune(runes[j]) {
			j++
		}
		aliasStart := j
		for j < n && isIdentRune(runes[j]) {
			j++
		}
		if j == aliasStart || (j < n && isIdentRune(runes[j])) {
			i++
			continue
		}

		oldAlias := string(runes[aliasStart:j])
		expr := extractExprBeforeRunes(runes, i)
		if expr != "" {
			canonical := buildCanonicalAlias(expr)
			if canonical != "" && !strings.EqualFold(oldAlias, canonical) {
				result[strings.ToUpper(oldAlias)] = canonical
			}
		}
		i = j
	}

	return result
}

// extractSelectPart returns the SELECT field list (before ИЗ/FROM/ПОМЕСТИТЬ/INTO).
func extractSelectPart(stmt string) string {
	end := findSelectEnd(stmt)
	if end < 0 {
		return stmt
	}
	return stmt[:end]
}

// extractTempTableName returns the temp table name from ПОМЕСТИТЬ/INTO, or "".
func extractTempTableName(stmt string) string {
	runes := []rune(stmt)
	n := len(runes)
	for i := 0; i < n; i++ {
		if matchWordAt(runes, i, []rune("ПОМЕСТИТЬ")) {
			return extractIdentAfter(runes, i+9)
		}
		if matchWordAt(runes, i, []rune("INTO")) {
			return extractIdentAfter(runes, i+4)
		}
	}
	return ""
}

// matchWordAt checks if runes[pos:] starts with word (case-insensitive) at a word boundary.
func matchWordAt(runes []rune, pos int, word []rune) bool {
	n := len(runes)
	wLen := len(word)
	if pos+wLen > n {
		return false
	}
	for k := 0; k < wLen; k++ {
		if unicode.ToUpper(runes[pos+k]) != unicode.ToUpper(word[k]) {
			return false
		}
	}
	if pos > 0 && isIdentRune(runes[pos-1]) {
		return false
	}
	if pos+wLen < n && isIdentRune(runes[pos+wLen]) {
		return false
	}
	return true
}

// extractIdentAfter skips whitespace and returns the next identifier.
func extractIdentAfter(runes []rune, pos int) string {
	n := len(runes)
	for pos < n && isSpaceRune(runes[pos]) {
		pos++
	}
	start := pos
	for pos < n && isIdentRune(runes[pos]) {
		pos++
	}
	if pos == start {
		return ""
	}
	return string(runes[start:pos])
}

// applyTempRenames replaces field references like Alias.OldField with Alias.NewField.
func applyTempRenames(stmt string, tempRenames map[string]map[string]string) string {
	if len(tempRenames) == 0 {
		return stmt
	}

	// Build alias→renames from explicit FROM aliases and direct temp table names
	aliasRenameMap := make(map[string]map[string]string)

	tableAliasMap := parseFromAliases(stmt)
	for tableAlias, tableName := range tableAliasMap {
		if renames, ok := tempRenames[strings.ToUpper(tableName)]; ok {
			aliasRenameMap[strings.ToUpper(tableAlias)] = renames
		}
	}

	// Direct temp table name usage (ИЗ ВТ1 without КАК)
	for tmpName, renames := range tempRenames {
		if _, already := aliasRenameMap[tmpName]; !already {
			aliasRenameMap[tmpName] = renames
		}
	}
	if len(aliasRenameMap) == 0 {
		return stmt
	}

	// Replace Alias.OldField → Alias.NewField
	runes := []rune(stmt)
	n := len(runes)
	var b strings.Builder
	b.Grow(len(stmt) + 64)

	i := 0
	for i < n {
		if !isIdentRune(runes[i]) {
			b.WriteRune(runes[i])
			i++
			continue
		}

		start := i
		for i < n && isIdentRune(runes[i]) {
			i++
		}
		ident1 := string(runes[start:i])

		if i >= n || runes[i] != '.' {
			b.WriteString(ident1)
			continue
		}

		dotPos := i
		i++

		start2 := i
		for i < n && isIdentRune(runes[i]) {
			i++
		}
		if i == start2 {
			b.WriteString(ident1)
			b.WriteRune('.')
			continue
		}
		ident2 := string(runes[start2:i])

		if renames, ok := aliasRenameMap[strings.ToUpper(ident1)]; ok {
			if newField, ok := renames[strings.ToUpper(ident2)]; ok {
				b.WriteString(ident1)
				b.WriteRune('.')
				b.WriteString(newField)
				continue
			}
		}

		b.WriteString(string(runes[start : dotPos+1]))
		b.WriteString(ident2)
	}

	return b.String()
}

// parseFromAliases extracts {tableAlias → tableName} from the FROM/ИЗ clause.
func parseFromAliases(stmt string) map[string]string {
	fromStart := findSelectEnd(stmt)
	if fromStart < 0 {
		return nil
	}

	fromPart := stmt[fromStart:]
	runes := []rune(fromPart)
	n := len(runes)
	result := make(map[string]string)

	i := 0
	for i < n {
		kwLen, isKW := matchAliasKeyword(runes, i)
		if kwLen == 0 || !isKW {
			i++
			continue
		}
		if i > 0 && !isSpaceRune(runes[i-1]) {
			i++
			continue
		}

		kwEnd := i + kwLen
		j := kwEnd
		for j < n && isSpaceRune(runes[j]) {
			j++
		}
		aliasStart := j
		for j < n && isIdentRune(runes[j]) {
			j++
		}
		if j == aliasStart {
			i++
			continue
		}
		alias := string(runes[aliasStart:j])
		tableName := extractTableNameBefore(runes, i)
		if tableName != "" {
			result[alias] = tableName
		}
		i = j
	}

	return result
}

func extractTableNameBefore(runes []rune, kakStart int) string {
	pos := kakStart - 1
	for pos >= 0 && isSpaceRune(runes[pos]) {
		pos--
	}
	if pos < 0 {
		return ""
	}
	end := pos + 1
	for pos >= 0 && (isIdentRune(runes[pos]) || runes[pos] == '.') {
		pos--
	}
	return string(runes[pos+1 : end])
}

// normalizeSingleQuery normalizes aliases in SELECT clauses of a single query,
// including all UNION/ОБЪЕДИНИТЬ branches.
func normalizeSingleQuery(query string) string {
	branches := splitByUnionClean(query)
	if len(branches) == 1 {
		return normalizeBranch(branches[0].text)
	}

	var b strings.Builder
	b.Grow(len(query) + 128)
	for i, br := range branches {
		if i > 0 {
			b.WriteString(br.separator)
		}
		b.WriteString(normalizeBranch(br.text))
	}
	return b.String()
}

type queryBranch struct {
	text      string
	separator string // "ОБЪЕДИНИТЬ", "ОБЪЕДИНИТЬ ВСЕ", "UNION", "UNION ALL" with surrounding whitespace
}

// splitByUnionClean splits query by top-level ОБЪЕДИНИТЬ/UNION, returning branch texts
// and the separators between them.
func splitByUnionClean(query string) []queryBranch {
	runes := []rune(query)
	n := len(runes)
	depth := 0

	type segment struct {
		startRune int
		endRune   int
	}
	type sep struct {
		startRune int
		endRune   int
	}

	var branchStarts []int // rune positions where branches start
	var separators []sep   // separators between branches

	branchStarts = append(branchStarts, 0)

	unionKWs := [][]rune{
		[]rune("ОБЪЕДИНИТЬ"),
		[]rune("UNION"),
	}
	allKWs := [][]rune{
		[]rune("ВСЕ"),
		[]rune("ALL"),
	}

	for i := 0; i < n; i++ {
		switch runes[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		if depth > 0 {
			continue
		}

		for _, uw := range unionKWs {
			if !matchWordAt(runes, i, uw) {
				continue
			}
			sepStart := i
			sepEnd := i + len(uw)

			// Check for ВСЕ/ALL
			j := sepEnd
			for j < n && isSpaceRune(runes[j]) {
				j++
			}
			for _, aw := range allKWs {
				if matchWordAt(runes, j, aw) {
					sepEnd = j + len(aw)
					break
				}
			}

			separators = append(separators, sep{sepStart, sepEnd})
			branchStarts = append(branchStarts, sepEnd)
			i = sepEnd - 1 // -1 because for loop increments
			break
		}
	}

	if len(branchStarts) == 1 {
		return []queryBranch{{text: query}}
	}

	var result []queryBranch
	for idx, start := range branchStarts {
		var end int
		if idx < len(separators) {
			end = runeOffsetToByteOffset(query, separators[idx].startRune)
		} else {
			end = len(query)
		}
		startByte := runeOffsetToByteOffset(query, start)
		text := query[startByte:end]

		var sepText string
		if idx > 0 {
			s := separators[idx-1]
			sepText = string(runes[s.startRune:s.endRune])
		}

		result = append(result, queryBranch{text: text, separator: sepText})
	}

	return result
}

// normalizeBranch normalizes aliases in a single SELECT...FROM branch.
func normalizeBranch(branch string) string {
	selectEnd := findSelectEnd(branch)
	if selectEnd < 0 {
		return normalizeAliasesInFragment(branch)
	}
	return normalizeAliasesInFragment(branch[:selectEnd]) + branch[selectEnd:]
}

// findSelectEnd returns the byte offset where the SELECT field list ends.
func findSelectEnd(query string) int {
	runes := []rune(query)
	n := len(runes)
	depth := 0

	for i := 0; i < n; i++ {
		switch runes[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		if depth > 0 {
			continue
		}

		if matchWordAt(runes, i, []rune("ИЗ")) {
			return runeOffsetToByteOffset(query, i)
		}
		if matchWordAt(runes, i, []rune("FROM")) {
			return runeOffsetToByteOffset(query, i)
		}
		if matchWordAt(runes, i, []rune("ПОМЕСТИТЬ")) {
			return runeOffsetToByteOffset(query, i)
		}
		if matchWordAt(runes, i, []rune("INTO")) {
			return runeOffsetToByteOffset(query, i)
		}
	}
	return -1
}

func runeOffsetToByteOffset(s string, runeIdx int) int {
	byteOff := 0
	for i := 0; i < runeIdx; i++ {
		_, size := utf8.DecodeRuneInString(s[byteOff:])
		byteOff += size
	}
	return byteOff
}

// normalizeAliasesInFragment normalizes КАК/AS aliases within a query fragment.
// Skips КАК inside parentheses (handles ВЫРАЗИТЬ(X КАК Тип)).
func normalizeAliasesInFragment(fragment string) string {
	runes := []rune(fragment)
	n := len(runes)
	var b strings.Builder
	b.Grow(len(fragment) + 64)

	depth := 0
	i := 0
	for i < n {
		switch runes[i] {
		case '(':
			depth++
			b.WriteRune(runes[i])
			i++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			b.WriteRune(runes[i])
			i++
			continue
		}

		// Skip КАК inside parentheses (ВЫРАЗИТЬ(X КАК Тип))
		if depth > 0 {
			b.WriteRune(runes[i])
			i++
			continue
		}

		kwLen, isKW := matchAliasKeyword(runes, i)
		if kwLen == 0 || !isKW {
			b.WriteRune(runes[i])
			i++
			continue
		}

		if i > 0 && !isSpaceRune(runes[i-1]) {
			b.WriteRune(runes[i])
			i++
			continue
		}

		kwEnd := i + kwLen
		j := kwEnd
		for j < n && isSpaceRune(runes[j]) {
			j++
		}
		if j >= n {
			b.WriteRune(runes[i])
			i++
			continue
		}

		aliasStart := j
		for j < n && isIdentRune(runes[j]) {
			j++
		}
		if j == aliasStart {
			b.WriteRune(runes[i])
			i++
			continue
		}
		aliasEnd := j
		_ = aliasStart

		if aliasEnd < n && isIdentRune(runes[aliasEnd]) {
			b.WriteRune(runes[i])
			i++
			continue
		}

		expr := extractExprBeforeRunes(runes, i)
		if expr == "" {
			for k := i; k < aliasEnd; k++ {
				b.WriteRune(runes[k])
			}
			i = aliasEnd
			continue
		}

		canonical := buildCanonicalAlias(expr)
		if canonical == "" {
			for k := i; k < aliasEnd; k++ {
				b.WriteRune(runes[k])
			}
			i = aliasEnd
			continue
		}

		for k := i; k < kwEnd; k++ {
			b.WriteRune(runes[k])
		}
		b.WriteByte(' ')
		b.WriteString(canonical)
		i = aliasEnd
	}

	return b.String()
}

// matchAliasKeyword checks if runes[pos:] starts with КАК or AS (case-insensitive).
func matchAliasKeyword(runes []rune, pos int) (int, bool) {
	n := len(runes)
	if pos+3 <= n {
		r0 := unicode.ToUpper(runes[pos])
		r1 := unicode.ToUpper(runes[pos+1])
		r2 := unicode.ToUpper(runes[pos+2])
		if r0 == 'К' && r1 == 'А' && r2 == 'К' {
			if pos+3 >= n || isSpaceRune(runes[pos+3]) {
				return 3, true
			}
		}
	}
	if pos+2 <= n {
		r0 := unicode.ToUpper(runes[pos])
		r1 := unicode.ToUpper(runes[pos+1])
		if r0 == 'A' && r1 == 'S' {
			if pos+2 >= n || isSpaceRune(runes[pos+2]) {
				return 2, true
			}
		}
	}
	return 0, false
}

func extractExprBeforeRunes(runes []rune, kakStart int) string {
	pos := kakStart - 1
	for pos >= 0 && isSpaceRune(runes[pos]) {
		pos--
	}
	if pos < 0 {
		return ""
	}
	end := pos + 1

	if runes[pos] == ')' {
		depth := 0
		for pos >= 0 {
			switch runes[pos] {
			case ')':
				depth++
			case '(':
				depth--
				if depth == 0 {
					goto foundOpen
				}
			}
			pos--
		}
		return ""
	foundOpen:
		pos--
		for pos >= 0 && isSpaceRune(runes[pos]) {
			pos--
		}
		for pos >= 0 && isIdentRune(runes[pos]) {
			pos--
		}
		return string(runes[pos+1 : end])
	}

	for pos >= 0 && (isIdentRune(runes[pos]) || runes[pos] == '.') {
		pos--
	}
	result := string(runes[pos+1 : end])
	if result == "" {
		return ""
	}
	return result
}

func buildCanonicalAlias(expr string) string {
	// Extract tokens (split by non-ident chars), filter out КАК/AS keywords
	tokens := splitIntoTokens(expr)
	var b strings.Builder
	b.Grow(len(expr))

	for _, tok := range tokens {
		upper := strings.ToUpper(tok)
		if upper == "КАК" || upper == "AS" {
			continue // skip type-cast keywords from ВЫРАЗИТЬ(X КАК Тип)
		}
		if b.Len() > 0 {
			// Capitalize first letter of subsequent tokens
			runes := []rune(tok)
			runes[0] = unicode.ToUpper(runes[0])
			tok = string(runes)
		}
		b.WriteString(tok)
	}

	s := b.String()
	if s == "" {
		return ""
	}
	first, _ := utf8.DecodeRuneInString(s)
	if !unicode.IsLetter(first) && first != '_' {
		return ""
	}
	return s
}

// splitIntoTokens splits expression into identifier tokens (by non-ident chars).
func splitIntoTokens(expr string) []string {
	var tokens []string
	var cur []rune
	for _, r := range expr {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			cur = append(cur, r)
		} else {
			if len(cur) > 0 {
				tokens = append(tokens, string(cur))
				cur = cur[:0]
			}
		}
	}
	if len(cur) > 0 {
		tokens = append(tokens, string(cur))
	}
	return tokens
}

func isSpaceRune(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n'
}

func isIdentRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// stripPresentationCalls removes calls to ПРЕДСТАВЛЕНИЕ/ПРЕДСТАВЛЕНИЕССЫЛКИ/
// PRESENTATION/REFPRESENTATION, replacing NAME(expr) with expr.
//
// Rationale: ПРЕДСТАВЛЕНИЕ() converts a reference to a string in 1C.
// For type-aware masking to work, the gateway must receive the reference
// itself (so it can be classified by metadata type). Stripping these calls
// before sending the query to 1C preserves the reference type in the result.
//
// Behavior:
//   - Case-insensitive matching on a fixed banlist.
//   - Respects nested parentheses (the argument may be ВЫБОР ... КОНЕЦ).
//   - Skips string literals "..." (with "" escape) and // comments.
//   - Runs up to 8 passes to handle nested calls; stops when stable.
//   - An identifier named Представление without a following ( is left alone.
func stripPresentationCalls(query string) string {
	const maxIterations = 8
	for iter := 0; iter < maxIterations; iter++ {
		next := stripPresentationPass(query)
		if next == query {
			return query
		}
		query = next
	}
	return query
}

// stripPresentationPass performs one pass of presentation-call removal.
func stripPresentationPass(query string) string {
	banlist := [][]rune{
		[]rune("ПРЕДСТАВЛЕНИЕССЫЛКИ"),
		[]rune("ПРЕДСТАВЛЕНИЕ"),
		[]rune("REFPRESENTATION"),
		[]rune("PRESENTATION"),
	}

	runes := []rune(query)
	n := len(runes)
	var b strings.Builder
	b.Grow(len(query))

	i := 0
	for i < n {
		r := runes[i]

		// Skip // comments until end of line
		if r == '/' && i+1 < n && runes[i+1] == '/' {
			for i < n && runes[i] != '\n' {
				b.WriteRune(runes[i])
				i++
			}
			continue
		}

		// Skip "..." string literals (with "" escape)
		if r == '"' {
			b.WriteRune(runes[i])
			i++
			for i < n {
				if runes[i] == '"' {
					// Doubled quote → escaped, stay inside literal
					if i+1 < n && runes[i+1] == '"' {
						b.WriteRune(runes[i])
						b.WriteRune(runes[i+1])
						i += 2
						continue
					}
					b.WriteRune(runes[i])
					i++
					break
				}
				b.WriteRune(runes[i])
				i++
			}
			continue
		}

		// Try to match a banlist keyword at word boundary
		if !isIdentRune(r) || (i > 0 && isIdentRune(runes[i-1])) {
			b.WriteRune(runes[i])
			i++
			continue
		}

		matchedLen := 0
		for _, kw := range banlist {
			if matchWordAt(runes, i, kw) {
				matchedLen = len(kw)
				break
			}
		}
		if matchedLen == 0 {
			b.WriteRune(runes[i])
			i++
			continue
		}

		// After the identifier, look for '(' (whitespace between is allowed).
		j := i + matchedLen
		k := j
		for k < n && isSpaceRune(runes[k]) {
			k++
		}
		if k >= n || runes[k] != '(' {
			// Just an identifier named Представление etc. — leave it.
			for p := i; p < j; p++ {
				b.WriteRune(runes[p])
			}
			i = j
			continue
		}

		// Find matching ')' respecting nested parens and string literals.
		argStart := k + 1
		argEnd, ok := findMatchingParen(runes, k)
		if !ok {
			// Unbalanced — leave as-is.
			b.WriteRune(runes[i])
			i++
			continue
		}

		// Emit the argument content (inner runes between parens), trimmed.
		inner := strings.TrimSpace(string(runes[argStart:argEnd]))
		if inner == "" {
			// ПРЕДСТАВЛЕНИЕ() with empty arg — drop the call entirely.
			i = argEnd + 1
			continue
		}
		b.WriteString(inner)
		i = argEnd + 1
	}

	return b.String()
}

// findMatchingParen finds the index of the ')' matching the '(' at openIdx.
// Respects nested parens, "..." string literals (with "" escape), and //
// comments. Returns (closeIdx, true) on success, (0, false) if unbalanced.
func findMatchingParen(runes []rune, openIdx int) (int, bool) {
	n := len(runes)
	if openIdx >= n || runes[openIdx] != '(' {
		return 0, false
	}
	depth := 1
	i := openIdx + 1
	for i < n {
		r := runes[i]

		if r == '/' && i+1 < n && runes[i+1] == '/' {
			for i < n && runes[i] != '\n' {
				i++
			}
			continue
		}

		if r == '"' {
			i++
			for i < n {
				if runes[i] == '"' {
					if i+1 < n && runes[i+1] == '"' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			continue
		}

		if r == '(' {
			depth++
		} else if r == ')' {
			depth--
			if depth == 0 {
				return i, true
			}
		}
		i++
	}
	return 0, false
}
