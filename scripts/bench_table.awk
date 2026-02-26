BEGIN {
	esc = sprintf("%c", 27)
	green = (USE_COLOR == 1) ? esc "[32m" : ""
	red = (USE_COLOR == 1) ? esc "[31m" : ""
	yellow = (USE_COLOR == 1) ? esc "[33m" : ""
	reset = (USE_COLOR == 1) ? esc "[0m" : ""

	caseWidth = 24
	implWidth = 8
	winnerWidth = 10
}

/^goos: / {
	goos = $2
}

/^goarch: / {
	goarch = $2
}

/^cpu: / {
	cpu = substr($0, 6)
}

/^BenchmarkExtract_/ {
	split($1, parts, "/")
	caseName = parts[1]
	sub(/^BenchmarkExtract_/, "", caseName)

	impl = parts[2]
	sub(/-[0-9]+$/, "", impl)
	if (impl != "decoder" && impl != "struct" && impl != "gjson" && impl != "fastjson") {
		next
	}

	if (!(caseName in seen)) {
		seen[caseName] = 1
		order[++count] = caseName
		if (length(caseName) > caseWidth) {
			caseWidth = length(caseName)
		}
	}

	ns[caseName, impl] = $3 + 0
	bop[caseName, impl] = $5 + 0
	alloc[caseName, impl] = $7 + 0
}

function exists(arr, caseName, impl) {
	return ((caseName SUBSEP impl) in arr)
}

function metricWinner(hasD, d, hasS, s, hasG, g, hasF, f,    min, winners, count) {
	min = -1
	if (hasD) {
		min = d
	}
	if (hasS && (min < 0 || s < min)) {
		min = s
	}
	if (hasG && (min < 0 || g < min)) {
		min = g
	}
	if (hasF && (min < 0 || f < min)) {
		min = f
	}
	if (min < 0) {
		return "n/a"
	}

	count = 0
	winners = ""
	if (hasD && d == min) {
		winners = winners "decoder"
		count++
	}
	if (hasS && s == min) {
		if (winners != "") {
			winners = winners ","
		}
		winners = winners "struct"
		count++
	}
	if (hasG && g == min) {
		if (winners != "") {
			winners = winners ","
		}
		winners = winners "gjson"
		count++
	}
	if (hasF && f == min) {
		if (winners != "") {
			winners = winners ","
		}
		winners = winners "fastjson"
		count++
	}

	if (count > 1) {
		return "tie"
	}
	return winners
}

function sideStatus(hasV, v, hasD, d, hasS, s, hasG, g, hasF, f,    min, minCount) {
	if (!hasV) {
		return "na"
	}

	min = -1
	if (hasD) {
		min = d
	}
	if (hasS && (min < 0 || s < min)) {
		min = s
	}
	if (hasG && (min < 0 || g < min)) {
		min = g
	}
	if (hasF && (min < 0 || f < min)) {
		min = f
	}

	if (min < 0) {
		return "na"
	}

	minCount = 0
	if (hasD && d == min) {
		minCount++
	}
	if (hasS && s == min) {
		minCount++
	}
	if (hasG && g == min) {
		minCount++
	}
	if (hasF && f == min) {
		minCount++
	}

	if (v == min) {
		if (minCount > 1) {
			return "tie"
		}
		return "win"
	}

	return "lose"
}

function paint(text, status) {
	if (status == "win") {
		return green text reset
	}
	if (status == "lose") {
		return red text reset
	}
	if (status == "tie") {
		return yellow text reset
	}
	return text
}

function fmtNs(v) {
	if (v >= 1000) {
		return sprintf("%.0f", v)
	}
	return sprintf("%.1f", v)
}

function fmtInt(v) {
	return sprintf("%.0f", v)
}

function metricText(hasV, v, kind) {
	if (!hasV) {
		return "n/a"
	}
	return (kind == "ns") ? fmtNs(v) : fmtInt(v)
}

function repeat(ch, times,    out, i) {
	out = ""
	for (i = 0; i < times; i++) {
		out = out ch
	}
	return out
}

function printMetricTable(title, arr, kind,    i, caseName, hasD, hasS, hasG, hasF, d, s, g, f, dText, sText, gText, fText, dStatus, sStatus, gStatus, fStatus, winner) {
	print ""
	printf("== %s ==\n", title)

	printf("%-*s | %-*s | %-*s | %-*s | %-*s | %-*s\n",
		caseWidth, "Case",
		implWidth, "decoder",
		implWidth, "struct",
		implWidth, "gjson",
		implWidth, "fjson",
		winnerWidth, "winner")

	printf("%s-+-%s-+-%s-+-%s-+-%s-+-%s\n",
		repeat("-", caseWidth),
		repeat("-", implWidth),
		repeat("-", implWidth),
		repeat("-", implWidth),
		repeat("-", implWidth),
		repeat("-", winnerWidth))

	for (i = 1; i <= count; i++) {
		caseName = order[i]

		hasD = exists(arr, caseName, "decoder")
		hasS = exists(arr, caseName, "struct")
		hasG = exists(arr, caseName, "gjson")
		hasF = exists(arr, caseName, "fastjson")

		d = arr[caseName, "decoder"]
		s = arr[caseName, "struct"]
		g = arr[caseName, "gjson"]
		f = arr[caseName, "fastjson"]

		dText = metricText(hasD, d, kind)
		sText = metricText(hasS, s, kind)
		gText = metricText(hasG, g, kind)
		fText = metricText(hasF, f, kind)

		dStatus = sideStatus(hasD, d, hasD, d, hasS, s, hasG, g, hasF, f)
		sStatus = sideStatus(hasS, s, hasD, d, hasS, s, hasG, g, hasF, f)
		gStatus = sideStatus(hasG, g, hasD, d, hasS, s, hasG, g, hasF, f)
		fStatus = sideStatus(hasF, f, hasD, d, hasS, s, hasG, g, hasF, f)

		dText = paint(sprintf("%*s", implWidth, dText), dStatus)
		sText = paint(sprintf("%*s", implWidth, sText), sStatus)
		gText = paint(sprintf("%*s", implWidth, gText), gStatus)
		fText = paint(sprintf("%*s", implWidth, fText), fStatus)

		winner = metricWinner(hasD, d, hasS, s, hasG, g, hasF, f)

		printf("%-*s | %s | %s | %s | %s | %-*s\n",
			caseWidth, caseName,
			dText,
			sText,
			gText,
			fText,
			winnerWidth, winner)
	}
}

END {
	if (count == 0) {
		print "no benchmark rows found"
		exit 0
	}

	if (goos != "" || goarch != "" || cpu != "") {
		printf("env: %s/%s | cpu: %s\n", goos, goarch, cpu)
	}

	printMetricTable("ns/op", ns, "ns")
	printMetricTable("B/op", bop, "int")
	printMetricTable("allocs/op", alloc, "int")
}
