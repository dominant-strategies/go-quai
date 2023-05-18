## Quai Recovery Mechanism

We need a recovery mechanism to recover from the dead state, execute a hard
fork. Since this is reasonable to expect during testnets and even during
mainnets.

### How to do it?
#### Part A - Recovery mechanism given a checkpoint
1. Add a hash in chain to the bad hashes list
    - To do this, we need the last referenceable prime block in each slice and
      add the hash of the next block in the canonical chain to the bad hashes
      list.
    - On startup during the recovery process, purge the database to that given
      hash
    - Set the current Header to that prime block
    - Reject bad hashes block in Append, so that the old chain can never get
      into the chain again, we should not even write those blocks to the
      database. 
2. Create a method to generate pending header in all slices
    - After the state reset, need to be able to generate pending headers in all
      slices without the help of ph cache
3. After this the node should be able to restart and produce blocks

#### Part B - Checkpoint Process
Many ways this can be done, Ethereum does this in the downloader where they
checkpoint based on a random frequency. We can do it that way too, or based on
a fixed frequency, or based on fixed number of prime blocks. This is open for discussion.



