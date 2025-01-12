#!/bin/bash

# turns the metrictank-sample.ini into a nice markdown document.
# headers like h3 and h2 are printed as-is, and the lines between them (config items and their comments)
# are wrapped in ``` blocks. t=code means we're in such a block.

function process() {
	local filename="$1"
t=
while read line; do
	# skip empty lines
	if [ "$line" == "" ]; then
		continue
	fi
	if [[ "$line" =~ ^### ]]; then
		if [ "$t" == code ]; then
			echo '```'
			echo
		fi
		echo "$line"
		t=h3
	elif [[ "$line" =~ ^## ]]; then
		if [ "$t" == code ]; then
			echo '```'
			echo
		fi
		echo "$line"
		t=h2
	else
		if [ "$t" == h2 -o "$t" == h3 ]; then
			echo -e '\n```'
			t=code
		fi
		# lines that start with a pound are fine within code blocks,
		# but outside of code blocks, would be shown as headers, which is not how they are intended
		# in the source file they are just regular comments too, so take away their #
		#if [[ "$line" =~ ^# ]]; then
		if [[ "$t" != code ]]; then
			sed -e 's/^# //' -e 's/$/  /'<<< "$line"
		else
			echo "$line"
		fi
	fi
done < "$filename"

# finish of pending code block
if [ "$t" == code ]; then
	echo '```'
fi
}

cat << EOF
# Config

Metrictank comes with an [example main config file](https://github.com/grafana/metrictank/blob/master/metrictank-sample.ini),
a [storage-schemas.conf file](https://github.com/grafana/metrictank/blob/master/scripts/config/storage-schemas.conf) and
a [storage-aggregation.conf file](https://github.com/grafana/metrictank/blob/master/scripts/config/storage-aggregation.conf)

The files themselves are well documented, but for your convenience, they are replicated below.  

Config values for the main ini config file can also be set, or overridden via environment variables.
They require the 'MT_' prefix.  Any delimiter is represented as an underscore.
Settings within section names in the config just require you to prefix the section header.

Examples:

\`\`\`
MT_LOG_LEVEL: 1                           # MT_<setting_name>
MT_CASSANDRA_WRITE_CONCURRENCY: 10        # MT_<setting_name>
MT_KAFKA_MDM_IN_DATA_DIR: /your/data/dir  # MT_<section_title>_<setting_name>
\`\`\`

---


# Metrictank.ini


EOF

process metrictank-sample.ini

cat << EOF

# storage-schemas.conf

\`\`\`
EOF
cat scripts/config/storage-schemas.conf

cat << EOF
\`\`\`

# storage-aggregation.conf

\`\`\`
EOF

cat scripts/config/storage-aggregation.conf

cat << EOF
\`\`\`

This file is generated by [config-to-doc](https://github.com/grafana/metrictank/blob/master/scripts/config-to-doc.sh)

EOF
