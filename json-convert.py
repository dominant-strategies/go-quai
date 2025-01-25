#!/usr/bin/env python3
import json
import sys

def convert_json_to_single_line(input_file, output_file):
    with open(input_file, 'r') as f:
        data = json.load(f)

    result = []
    for address, details in data.items():
        balance_str = details.get("balance", "0")
        result.append({
            "Vest Schedule": 1,
            "Address": address,
            "Amount": int(balance_str)
        })

    with open(output_file, 'w') as f:
        json.dump(result, f, separators=(',', ':'))

if __name__ == '__main__':
    if len(sys.argv) < 3:
        print(f"Usage: {sys.argv[0]} <input.json> <output.json>")
        sys.exit(1)

    input_file = sys.argv[1]
    output_file = sys.argv[2]
    convert_json_to_single_line(input_file, output_file)

