package render

// Text holds every human-facing string of the comment. Levels are one action word each; the legend spells out the meaning.
type Text struct {
	Title, Range, ReadOnly, Summary, Findings, NoFindings, Level, Location, Finding, Suggestion string
	Legend, Resolved, Coverage, NextPrompt, CopyHint, NoFollowUp, Footer, Truncated             string
	Verdict, Severity, Meaning, Mode                                                            map[string]string
}

var texts = map[string]Text{
	"en": {
		Title: "Automated review", Range: "Range", ReadOnly: "**Code reading only**, no test/lint/build",
		Summary: "Summary", Findings: "Findings", NoFindings: "No findings.",
		Level: "Level", Location: "Location", Finding: "Finding", Suggestion: "Suggestion", Legend: "Levels",
		Resolved: "Resolved since the last review", Coverage: "Review limits",
		NextPrompt: "Prompt for the next iteration", CopyHint: "Copy to an agent to act on this review",
		NoFollowUp: "(no follow-up)",
		Footer:     "_Automated review that only reads code. Merging stays a human decision; run test/lint/build before pushing._",
		Truncated:  "_(comment truncated because it was too long)_",
		Verdict: map[string]string{
			"safe_to_merge": "✅ Safe to merge", "needs_changes": "🟡 Needs changes", "unsafe": "🔴 Not safe to merge",
		},
		Severity: map[string]string{
			"critical": "🔴 Must-fix", "required": "🟠 Fix", "consider": "🔵 Consider", "nit": "⚪ Optional", "fyi": "💬 Note",
		},
		Meaning: map[string]string{
			"critical": "must be fixed, merging is unsafe", "required": "fix before merging",
			"consider": "worth considering, not blocking", "nit": "nice to have, fine to skip", "fyi": "context only, no action",
		},
		Mode: map[string]string{
			"full": "full review", "incremental": "incremental since the last review",
			"full-forcepush": "full review (history rewritten / force-push)",
		},
	},
	"id": {
		Title: "Review otomatis", Range: "Rentang", ReadOnly: "**Hanya baca kode**, tanpa test/lint/build",
		Summary: "Ringkasan", Findings: "Temuan", NoFindings: "Tidak ada temuan.",
		Level: "Level", Location: "Lokasi", Finding: "Temuan", Suggestion: "Saran", Legend: "Level",
		Resolved: "Sudah teratasi sejak review terakhir", Coverage: "Batasan review",
		NextPrompt: "Prompt untuk iterasi berikutnya", CopyHint: "Salin ke agent untuk menindaklanjuti review ini",
		NoFollowUp: "(tidak ada tindak lanjut)",
		Footer:     "_Review otomatis, hanya membaca kode. Keputusan merge tetap di manusia; jalankan test/lint/build sebelum push._",
		Truncated:  "_(komentar dipotong karena terlalu panjang)_",
		Verdict: map[string]string{
			"safe_to_merge": "✅ Aman untuk merge", "needs_changes": "🟡 Perlu penyesuaian", "unsafe": "🔴 Belum aman untuk merge",
		},
		Severity: map[string]string{
			"critical": "🔴 Wajib", "required": "🟠 Perbaiki", "consider": "🔵 Saran", "nit": "⚪ Sunnah", "fyi": "💬 Info",
		},
		Meaning: map[string]string{
			"critical": "wajib diperbaiki, merge tidak aman", "required": "perbaiki sebelum merge",
			"consider": "layak dipertimbangkan, tidak memblokir", "nit": "baik dilakukan, boleh dilewati", "fyi": "konteks saja, tanpa tindakan",
		},
		Mode: map[string]string{
			"full": "review penuh", "incremental": "inkremental sejak review terakhir",
			"full-forcepush": "review penuh (history berubah/force-push)",
		},
	},
}

// TextFor falls back to English for an unknown language.
func TextFor(lang string) Text {
	if t, ok := texts[lang]; ok {
		return t
	}
	return texts["en"]
}
