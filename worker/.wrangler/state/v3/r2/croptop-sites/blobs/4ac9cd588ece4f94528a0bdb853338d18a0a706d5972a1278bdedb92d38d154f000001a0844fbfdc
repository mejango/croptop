// CTPublisher (croptop-core-v6). Same address on every supported chain.
const croptopPublisherContract = () => "0xcbc84cf9b0293efe3ac7dd1bea128a404f2e6a1c";

// Posting allowance for a collection + category. Returns an ethers Result with named fields
// (minimumPrice, minimumTotalSupply, maximumTotalSupply, maximumSplitPercent, allowedAddresses), or null if unreachable.
const tx_view_allowance = async (hook, category, chainId) => {
  try {
    return await view(chainId, croptopPublisherContract(), CT_PUBLISHER_ABI, "allowanceFor", [hook, category]);
  } catch (e) {
    console.error("allowanceFor failed", e);
    return null;
  }
}

// The 721 tiers already recorded for these posts (id 0 = not recorded yet). Empty if unreachable.
const tx_view_tiers = async (hook, encodedIpfsUris, chainId) => {
  try {
    return await view(chainId, croptopPublisherContract(), CT_PUBLISHER_ABI, "tiersFor", [hook, encodedIpfsUris]);
  } catch (e) {
    console.error("tiersFor failed", e);
    return [];
  }
}

// Record and/or buy one copy of a post, paying `value` native token (price + Croptop fee).
// `split` = { percent (out of SPLITS_TOTAL_PERCENT), beneficiary }: that share of every sale of this post goes to
// the beneficiary instead of the shop's treasury. Only matters when the post is first recorded.
// ponytail: one copy per tx. v6 rejects the same post twice in a batch (CTPublisher_DuplicatePost).
const tx_collect = async (hook, category, totalSupply, price, encodedIpfsUri, beneficiary, feeBeneficiary, value, chainId, split = null) => {
  const splits = split?.percent ? [{ percent: SPLITS_TOTAL_PERCENT, projectId: 0, beneficiary: split.beneficiary, preferAddToBalance: false, lockedUntil: 0, hook: "0x0000000000000000000000000000000000000000" }] : [];
  const post = { encodedIpfsUri, totalSupply, price, category, splitPercent: split?.percent || 0, splits };
  return handleTransact(
    [chainId],
    async () => [hook, [post], NATIVE_TOKEN, value, beneficiary, feeBeneficiary, "0x"],
    croptopPublisherContract,
    CT_PUBLISHER_ABI,
    "mintFrom",
    value
  );
}
