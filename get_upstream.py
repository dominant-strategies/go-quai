from subprocess import getoutput

lines = []

# Commits to apply, in order of newest to oldest
with open('eth_commits.txt') as f:
    lines = f.readlines()

# Reverse commits to apply oldest first
lines.reverse()

# Iterate and fetch commit data from github using hub. Skip if merge conflict arises
# Log to file in order to manually resolve merge conflicts post apply.
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