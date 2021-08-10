from subprocess import getoutput

lines = []

with open('eth_commits.txt') as f:
    lines = f.readlines()

lines.reverse()

for line in lines:
    str = "hub am -3 https://github.com/ethereum/go-ethereum/commit/" + line
    response = getoutput(str)
    print(response)
    if "CONFLICT" in response or "to see the failed patch" in response:
        print("Responding")
        output = getoutput("git am --skip")
        print(output)
        f = open("error_commits.txt", "a")
        f.write("hub am -3 https://github.com/ethereum/go-ethereum/commit/" + line)