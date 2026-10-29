package agent

import (
	"regexp"
	"strings"
)

// actNowNudge follows a reply that only announced work: the turn ends with
// the reply, so the work has to happen now.
const actNowNudge = "[System] Your reply only announced what you would do, and the user is still waiting. " +
	"Nothing runs after your reply. Do it now: call the tools you need and answer with the results. " +
	"If you really need the user to choose something first, ask only that."

// announcePhrases are promises of work to come, in Spanish and English.
var announcePhrases = regexp.MustCompile(`(?i)\b(proceder[ée]|procedo a|voy a (consultar|buscar|revisar|analizar|verificar|obtener|calcular|generar|preparar)|` +
	`(un|dame un|en un) momento|esto puede (tomar|demorar|llevar)|en breve te|ahora (consulto|busco|reviso|verifico)|` +
	`i will (now )?(check|search|look|query|fetch|analy[sz]e)|i'?ll (now )?(check|search|look|query|fetch)|let me (check|look|search|query)|` +
	`this (may|might|can) take a moment)`)

// announcesWork reports a reply that promises work instead of doing it. A
// reply that ends asking the user something is a real question, not a
// promise.
func announcesWork(content string) bool {
	text := strings.TrimSpace(content)
	if text == "" || strings.HasSuffix(text, "?") {
		return false
	}
	return announcePhrases.MatchString(text)
}
