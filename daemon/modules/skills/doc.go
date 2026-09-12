// Package skills discovers skills from configured paths (including ~/.claude/skills),
// injects the skill index as a prefix context block at session start and after a
// summarize compaction, and registers skill.load so the model can read a skill body on
// demand.
package skills
