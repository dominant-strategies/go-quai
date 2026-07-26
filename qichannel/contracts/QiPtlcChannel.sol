// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/// @title QiPtlcChannel
/// @notice A two-party payment channel on the Quai EVM ledger that settles
/// point timelocked contracts (PTLCs) against the same payment-point space
/// used by Qi channels.
///
/// A routed payment is atomic across ledgers because every hop is locked to
/// the same kind of condition: reveal the scalar `t` such that `t*G == T`.
/// On Qi that condition is enforced by an adaptor signature; here it is
/// enforced by `pointAddress` below. A routing node holding a Qi channel on
/// one side and this contract on the other can therefore forward a payment
/// between the two ledgers, learning the secret from whichever side settles
/// first and using it to settle the other.
///
/// Unlike the Qi-side channels, this contract can adjudicate: a stale state
/// can be replaced during a challenge window by a state carrying a higher
/// nonce. Channels here are therefore perpetual, with no bounded update
/// count and no expiry.
contract QiPtlcChannel {
    // secp256k1 generator x coordinate and group order.
    uint256 private constant GX =
        0x79BE667EF9DCBBAC55A06295CE870B07029BFCDB2DCE28D959F2815B16F81798;
    uint256 private constant N =
        0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141;

    /// @notice The generator has an even y coordinate, so recovering against
    /// it uses v = 27.
    uint8 private constant GENERATOR_PARITY = 27;

    enum Status {
        Open,
        Closing,
        Closed
    }

    /// @param point   Address commitment to the payment point T, i.e. the
    ///                last 20 bytes of keccak256(T.x || T.y). This is exactly
    ///                what ecrecover yields for the point, which is what
    ///                makes the check below cheap.
    /// @param amount  Value held by this PTLC, in wei.
    /// @param deadline Block number after which the payer may reclaim it.
    /// @param toPartyB True if party B is the payee, false if party A is.
    struct Ptlc {
        address point;
        uint256 amount;
        uint64 deadline;
        bool toPartyB;
    }

    address public immutable partyA;
    address public immutable partyB;

    /// @notice Blocks a counterparty has to replace a stale closing state.
    uint64 public immutable challengePeriod;

    Status public status;
    uint256 public depositA;
    uint256 public depositB;

    /// @notice The closing state currently on record.
    uint256 public closingNonce;
    uint256 public closingBalanceA;
    uint256 public closingBalanceB;
    uint64 public challengeDeadline;
    Ptlc[] public closingPtlcs;
    /// @notice Tracks which PTLCs of the closing state have been resolved.
    mapping(uint256 => bool) public ptlcResolved;

    event Funded(address indexed party, uint256 amount);
    event ClosingStarted(uint256 indexed nonce, uint64 challengeDeadline);
    event ClosingChallenged(uint256 indexed nonce, uint64 challengeDeadline);
    /// @notice Emitted when a PTLC is claimed. The secret is published here
    /// deliberately: it is what lets the upstream hop settle its own leg, and
    /// is the mechanism that makes a routed payment atomic.
    event SecretRevealed(address indexed point, uint256 secret);
    event Settled(uint256 balanceA, uint256 balanceB);

    error NotAParty();
    error WrongStatus();
    error BadSignature();
    error StaleState();
    error ChallengeOngoing();
    error DeadlineNotReached();
    error DeadlinePassed();
    error AlreadyResolved();
    error BadScalar();
    error WrongSecret();
    error BalanceMismatch();
    error TransferFailed();

    modifier onlyParty() {
        if (msg.sender != partyA && msg.sender != partyB) revert NotAParty();
        _;
    }

    constructor(address _partyA, address _partyB, uint64 _challengePeriod) {
        require(_partyA != address(0) && _partyB != address(0) && _partyA != _partyB, "bad parties");
        require(_challengePeriod > 0, "bad challenge period");
        partyA = _partyA;
        partyB = _partyB;
        challengePeriod = _challengePeriod;
    }

    /// @notice Deposit funds into the channel. Only possible while open.
    function fund() external payable onlyParty {
        if (status != Status.Open) revert WrongStatus();
        if (msg.sender == partyA) {
            depositA += msg.value;
        } else {
            depositB += msg.value;
        }
        emit Funded(msg.sender, msg.value);
    }

    /// @notice Total value held by the channel.
    function capacity() public view returns (uint256) {
        return depositA + depositB;
    }

    /// @notice Hash of a channel state, signed by both parties off chain.
    /// Binding the contract address in prevents a state signed for one
    /// channel from being replayed against another.
    function stateHash(
        uint256 nonce,
        uint256 balanceA,
        uint256 balanceB,
        Ptlc[] memory ptlcs
    ) public view returns (bytes32) {
        return keccak256(abi.encode(address(this), block.chainid, nonce, balanceA, balanceB, ptlcs));
    }

    /// @notice Close immediately on a state both parties agree on. No
    /// challenge period is needed because neither party can be cheated by a
    /// state they just signed.
    function closeCooperative(
        uint256 nonce,
        uint256 balanceA,
        uint256 balanceB,
        bytes calldata signatureA,
        bytes calldata signatureB
    ) external onlyParty {
        if (status == Status.Closed) revert WrongStatus();
        bytes32 digest = stateHash(nonce, balanceA, balanceB, new Ptlc[](0));
        _requireBothSignatures(digest, signatureA, signatureB);
        if (balanceA + balanceB != capacity()) revert BalanceMismatch();

        status = Status.Closed;
        _payout(balanceA, balanceB);
    }

    /// @notice Begin a unilateral close with the latest state one holds. The
    /// counterparty may replace it with a higher-nonce state until the
    /// challenge deadline.
    function startClose(
        uint256 nonce,
        uint256 balanceA,
        uint256 balanceB,
        Ptlc[] calldata ptlcs,
        bytes calldata signatureA,
        bytes calldata signatureB
    ) external onlyParty {
        if (status != Status.Open) revert WrongStatus();
        _recordState(nonce, balanceA, balanceB, ptlcs, signatureA, signatureB);
        status = Status.Closing;
        emit ClosingStarted(nonce, challengeDeadline);
    }

    /// @notice Replace the recorded state with a strictly newer one. This is
    /// what makes stale states unprofitable, and why channels on this side
    /// need no expiry.
    function challengeClose(
        uint256 nonce,
        uint256 balanceA,
        uint256 balanceB,
        Ptlc[] calldata ptlcs,
        bytes calldata signatureA,
        bytes calldata signatureB
    ) external onlyParty {
        if (status != Status.Closing) revert WrongStatus();
        if (block.number > challengeDeadline) revert DeadlinePassed();
        if (nonce <= closingNonce) revert StaleState();
        _recordState(nonce, balanceA, balanceB, ptlcs, signatureA, signatureB);
        emit ClosingChallenged(nonce, challengeDeadline);
    }

    function _recordState(
        uint256 nonce,
        uint256 balanceA,
        uint256 balanceB,
        Ptlc[] calldata ptlcs,
        bytes calldata signatureA,
        bytes calldata signatureB
    ) private {
        bytes32 digest = stateHash(nonce, balanceA, balanceB, ptlcs);
        _requireBothSignatures(digest, signatureA, signatureB);

        uint256 locked;
        for (uint256 i = 0; i < ptlcs.length; i++) {
            locked += ptlcs[i].amount;
        }
        if (balanceA + balanceB + locked != capacity()) revert BalanceMismatch();

        // Clear any previously recorded PTLC resolutions before replacing.
        for (uint256 i = 0; i < closingPtlcs.length; i++) {
            delete ptlcResolved[i];
        }
        delete closingPtlcs;
        for (uint256 i = 0; i < ptlcs.length; i++) {
            closingPtlcs.push(ptlcs[i]);
        }

        closingNonce = nonce;
        closingBalanceA = balanceA;
        closingBalanceB = balanceB;
        challengeDeadline = uint64(block.number) + challengePeriod;
    }

    /// @notice Derive the address commitment of the point `scalar * G`.
    ///
    /// ecrecover(h, v, r, s) returns the address of `r^-1 * (s*R - h*G)`,
    /// where R is the curve point with x coordinate r and parity from v.
    /// Taking h = 0, r = GX and s = scalar*GX mod N makes R the generator and
    /// collapses the expression to `scalar * G`, so the precompile performs a
    /// scalar multiplication for about 3000 gas.
    function pointAddress(uint256 scalar) public pure returns (address) {
        if (scalar == 0 || scalar >= N) revert BadScalar();
        return ecrecover(bytes32(0), GENERATOR_PARITY, bytes32(GX), bytes32(mulmod(scalar, GX, N)));
    }

    /// @notice Claim a PTLC by revealing the scalar behind its payment point.
    /// The secret is emitted so the payer's upstream hop can observe it and
    /// settle its own leg.
    function claimPtlc(uint256 index, uint256 secret) external {
        if (status != Status.Closing) revert WrongStatus();
        if (index >= closingPtlcs.length) revert AlreadyResolved();
        if (ptlcResolved[index]) revert AlreadyResolved();

        Ptlc storage ptlc = closingPtlcs[index];
        if (block.number > ptlc.deadline) revert DeadlinePassed();
        if (pointAddress(secret) != ptlc.point) revert WrongSecret();

        ptlcResolved[index] = true;
        if (ptlc.toPartyB) {
            closingBalanceB += ptlc.amount;
        } else {
            closingBalanceA += ptlc.amount;
        }
        emit SecretRevealed(ptlc.point, secret);
    }

    /// @notice Reclaim a PTLC whose deadline has passed without a claim.
    function refundPtlc(uint256 index) external {
        if (status != Status.Closing) revert WrongStatus();
        if (index >= closingPtlcs.length) revert AlreadyResolved();
        if (ptlcResolved[index]) revert AlreadyResolved();

        Ptlc storage ptlc = closingPtlcs[index];
        if (block.number <= ptlc.deadline) revert DeadlineNotReached();

        ptlcResolved[index] = true;
        // The payer is whichever party is not the payee.
        if (ptlc.toPartyB) {
            closingBalanceA += ptlc.amount;
        } else {
            closingBalanceB += ptlc.amount;
        }
    }

    /// @notice Pay out the recorded state once the challenge window has
    /// closed and every PTLC has been claimed or refunded.
    function finalize() external {
        if (status != Status.Closing) revert WrongStatus();
        if (block.number <= challengeDeadline) revert ChallengeOngoing();
        for (uint256 i = 0; i < closingPtlcs.length; i++) {
            if (!ptlcResolved[i]) {
                // An unresolved PTLC past its deadline reverts to the payer.
                if (block.number <= closingPtlcs[i].deadline) revert DeadlineNotReached();
                ptlcResolved[i] = true;
                if (closingPtlcs[i].toPartyB) {
                    closingBalanceA += closingPtlcs[i].amount;
                } else {
                    closingBalanceB += closingPtlcs[i].amount;
                }
            }
        }
        status = Status.Closed;
        _payout(closingBalanceA, closingBalanceB);
    }

    function _payout(uint256 balanceA, uint256 balanceB) private {
        emit Settled(balanceA, balanceB);
        if (balanceA > 0) {
            (bool okA, ) = partyA.call{value: balanceA}("");
            if (!okA) revert TransferFailed();
        }
        if (balanceB > 0) {
            (bool okB, ) = partyB.call{value: balanceB}("");
            if (!okB) revert TransferFailed();
        }
    }

    function _requireBothSignatures(
        bytes32 digest,
        bytes calldata signatureA,
        bytes calldata signatureB
    ) private view {
        if (_recover(digest, signatureA) != partyA) revert BadSignature();
        if (_recover(digest, signatureB) != partyB) revert BadSignature();
    }

    function _recover(bytes32 digest, bytes calldata signature) private pure returns (address) {
        if (signature.length != 65) revert BadSignature();
        bytes32 r;
        bytes32 s;
        uint8 v;
        assembly {
            r := calldataload(signature.offset)
            s := calldataload(add(signature.offset, 32))
            v := byte(0, calldataload(add(signature.offset, 64)))
        }
        if (v < 27) v += 27;
        // Reject the malleable upper half of the s range.
        if (uint256(s) > 0x7FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF5D576E7357A4501DDFE92F46681B20A0) {
            revert BadSignature();
        }
        address recovered = ecrecover(digest, v, r, s);
        if (recovered == address(0)) revert BadSignature();
        return recovered;
    }

    /// @notice Number of PTLCs in the recorded closing state.
    function closingPtlcCount() external view returns (uint256) {
        return closingPtlcs.length;
    }
}
