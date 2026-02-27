BEGIN {
	esc = sprintf("%c", 27)
	green = (USE_COLOR == 1) ? esc "[32m" : ""
	red = (USE_COLOR == 1) ? esc "[31m" : ""
	yellow = (USE_COLOR == 1) ? esc "[33m" : ""
	reset = (USE_COLOR == 1) ? esc "[0m" : ""

	caseWidth = 24
	implWidth = 8
	winnerWidth = 12

	implCount = split("decoder struct gjson fastjson sonic go-json easyjson", impls, " ")
	for (i = 1; i <= implCount; i++) {
		allowed[impls[i]] = 1
		if (length(impls[i]) > implWidth) {
			implWidth = length(impls[i])
		}
	}
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
	if (!(impl in allowed)) {
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

function metricWinner(caseName, arr,    i, impl, min, v, winners, count) {
	min = -1
	for (i = 1; i <= implCount; i++) {
		impl = impls[i]
		if (!exists(arr, caseName, impl)) {
			continue
		}
		v = arr[caseName, impl] + 0
		if (min < 0 || v < min) {
			min = v
		}
	}
	if (min < 0) {
		return "n/a"
	}

	winners = ""
	count = 0
	for (i = 1; i <= implCount; i++) {
		impl = impls[i]
		if (!exists(arr, caseName, impl) || arr[caseName, impl] != min) {
			continue
		}
		if (winners != "") {
			winners = winners ","
		}
		winners = winners impl
		count++
	}

	if (count > 1) {
		return "tie"
	}
	return winners
}

function sideStatus(caseName, impl, arr,    i, cur, min, v, minCount) {
	if (!exists(arr, caseName, impl)) {
		return "na"
	}

	min = -1
	for (i = 1; i <= implCount; i++) {
		cur = impls[i]
		if (!exists(arr, caseName, cur)) {
			continue
		}
		v = arr[caseName, cur] + 0
		if (min < 0 || v < min) {
			min = v
		}
	}
	if (min < 0) {
		return "na"
	}

	minCount = 0
	for (i = 1; i <= implCount; i++) {
		cur = impls[i]
		if (exists(arr, caseName, cur) && arr[caseName, cur] == min) {
			minCount++
		}
	}

	if (arr[caseName, impl] == min) {
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

function printMetricTable(title, arr, kind,    i, j, caseName, impl, hasV, v, text, status, winner, row, header, sep) {
	print ""
	printf("== %s ==\n", title)

	header = sprintf("%-*s", caseWidth, "Case")
	sep = repeat("-", caseWidth)
	for (i = 1; i <= implCount; i++) {
		impl = impls[i]
		header = header " | " sprintf("%-*s", implWidth, impl)
		sep = sep "-+-" repeat("-", implWidth)
	}
	header = header " | " sprintf("%-*s", winnerWidth, "winner")
	sep = sep "-+-" repeat("-", winnerWidth)

	print header
	print sep

	for (i = 1; i <= count; i++) {
		caseName = order[i]
		row = sprintf("%-*s", caseWidth, caseName)
		for (j = 1; j <= implCount; j++) {
			impl = impls[j]
			hasV = exists(arr, caseName, impl)
			v = arr[caseName, impl]
			text = metricText(hasV, v, kind)
			status = sideStatus(caseName, impl, arr)
			row = row " | " paint(sprintf("%*s", implWidth, text), status)
		}
		winner = metricWinner(caseName, arr)
		row = row " | " sprintf("%-*s", winnerWidth, winner)
		print row
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
