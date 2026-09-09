// Juicebox v6 deployment addresses. Same on every supported chain unless keyed by chain id.
const revnetDeployerContract = () => "0xb552eb94284f94b833837d4b2cbb237128415d4e";
const JB_PROJECTS_ADDRESS = "0x6017d1fba9dc279bfa0b03fd931c22e242ab3691";

// Sucker deployers by "local-remote" chain pair. L1<->L2 pairs use the native bridge deployers,
// L2<->L2 pairs use CCIP. Testnets only have the L1<->L2 deployers.
const OP_SUCKER_DEPLOYER = "0x298a775c030adcedb641a89d9047ec9972674e1a";
const BASE_SUCKER_DEPLOYER = "0x54140331902de5c3445eb0c26e15099a5a9d59e6";
const ARB_SUCKER_DEPLOYER = "0xa12ebfca3d4e0810e4ed174e4c08277c26917acb";
const SUCKER_DEPLOYERS = {
  "1-10": OP_SUCKER_DEPLOYER, "10-1": OP_SUCKER_DEPLOYER,
  "1-8453": BASE_SUCKER_DEPLOYER, "8453-1": BASE_SUCKER_DEPLOYER,
  "1-42161": ARB_SUCKER_DEPLOYER, "42161-1": ARB_SUCKER_DEPLOYER,
  "10-42161": "0x1d58d56fbdb753de44737be926c33b79cf009afa", "42161-10": "0x1d58d56fbdb753de44737be926c33b79cf009afa",
  "10-8453": "0x8f6f0a70939997310309d7ab66b1b199faafe7f0", "8453-10": "0x8f6f0a70939997310309d7ab66b1b199faafe7f0",
  "42161-8453": "0x2845f919af9ed7d8dab188d42114bd590340a242", "8453-42161": "0x2845f919af9ed7d8dab188d42114bd590340a242",
  "11155111-11155420": OP_SUCKER_DEPLOYER, "11155420-11155111": OP_SUCKER_DEPLOYER,
  "11155111-84532": BASE_SUCKER_DEPLOYER, "84532-11155111": BASE_SUCKER_DEPLOYER,
  "11155111-421614": ARB_SUCKER_DEPLOYER, "421614-11155111": ARB_SUCKER_DEPLOYER,
};

const ZERO_ADDRESS = "0x0000000000000000000000000000000000000000";
const ZERO_BYTES32 = "0x0000000000000000000000000000000000000000000000000000000000000000";
const NATIVE_TOKEN_BYTES32 = "0x000000000000000000000000" + NATIVE_TOKEN.slice(2);
const PROJECT_URI = "ipfs://QmaJzQjaFKxU2LLyqPTpZoGU47owQcmmvVCP1p4YqeuMUy";

// JBProjects charges a native-token creation fee; REVDeployer.deployFor forwards msg.value to it.
const creationFeeOn = (chainId) =>
  view(chainId, JB_PROJECTS_ADDRESS, ["function creationFee() view returns (uint256)"], "creationFee", []);

// Bridge configs from `chainId` to every other selected chain.
const buildSuckerDeploymentConfigurations = (chainId, chainIds) =>
  chainIds.filter(id => id !== chainId).map(remoteChainId => {
    const deployer = SUCKER_DEPLOYERS[`${chainId}-${remoteChainId}`];
    if (!deployer) throw new Error(`Can't bridge between ${resolveChainLabel(chainId)} and ${resolveChainLabel(remoteChainId)}.`);
    return {
      deployer,
      peer: ZERO_BYTES32, // zero = the deterministic same-address peer
      mappings: [{ localToken: NATIVE_TOKEN, minGas: 200000, remoteToken: NATIVE_TOKEN_BYTES32 }],
    };
  });

// JB721TiersHookDeployer emits HookDeployed(uint256 indexed projectId, address hook, address caller).
const HOOK_DEPLOYED_TOPIC = ethers.id("HookDeployed(uint256,address,address)");
const hookAddressFromReceipt = (receipt) => {
  const log = receipt.logs.find(l => l.topics[0] === HOOK_DEPLOYED_TOPIC);
  return log ? ethers.getAddress("0x" + log.data.slice(26, 66)) : null;
}

// Deploy a revnet with a 721 hook and per-category Croptop posting rules on one chain (returns the hook address)
// or on several (returns a Relayr quote).
// `stages`: [{ startsAtOrAfter?, automintTokenAmount, initialIssuanceAmount, splitPercent, priceIncreasePercent,
//             priceIncreaseFrequency, cashOutTaxRate }]. Stage 1 starts 6 minutes from now on every chain.
const tx_deploy_revnet = async (deployment) => {
  const { chainIds, useCache = false } = deployment;
  const stage1StartsAt = Math.floor(Date.now() / 1000) + 6 * 60; // same on every chain
  const buildDeploymentData = async (chainId) => buildRevnetDeployArgs(deployment, chainId, stage1StartsAt);

  const receipt = await handleTransact(chainIds, buildDeploymentData, revnetDeployerContract, REV_DEPLOYER_ABI, "deployFor", creationFeeOn, useCache);
  if (!receipt) return false;
  if (receipt.payment_info || receipt.bundle_uuid) return receipt; // Relayr quote
  return hookAddressFromReceipt(receipt);
}

// The exact `deployFor` arguments for one chain. Pure, so it can be simulated with eth_call.
// `postingRules`: [{ category, minimumPrice, minimumTotalSupply, maximumTotalSupply (0 = no limit), maximumSplitPercent, allowedAddresses }]
const buildRevnetDeployArgs = ({ name, symbol, owner, postingRules, stages, chainIds, salt }, chainId, stage1StartsAt) => {
  const allowedPosts = postingRules.map(r => ({ maximumSplitPercent: 0, ...r }));
  {
    const ownerSplit = { percent: 1000000000, projectId: 0, beneficiary: owner, preferAddToBalance: false, lockedUntil: 0, hook: ZERO_ADDRESS };
    const stageConfigurations = stages.map((s, i) => ({
      startsAtOrAfter: i === 0 ? stage1StartsAt : s.startsAtOrAfter,
      autoIssuances: s.automintTokenAmount ? [{ chainId: Number(chainId), count: s.automintTokenAmount, beneficiary: owner }] : [],
      splitPercent: s.splitPercent,
      splits: s.splitPercent ? [ownerSplit] : [],
      initialIssuance: s.initialIssuanceAmount,
      issuanceCutFrequency: s.priceIncreaseFrequency,
      issuanceCutPercent: s.priceIncreasePercent,
      cashOutTaxRate: s.cashOutTaxRate,
      extraMetadata: 0,
    }));
    const configuration = {
      description: { name, ticker: symbol, uri: PROJECT_URI, salt },
      baseCurrency: NATIVE_TOKEN_CURRENCY,
      operator: owner,
      scopeCashOutsToLocalBalances: false,
      stageConfigurations,
    };
    const accountingContextsToAccept = [{ token: NATIVE_TOKEN, decimals: 18, currency: NATIVE_TOKEN_CURRENCY }];
    const suckerDeploymentConfiguration = { deployerConfigurations: buildSuckerDeploymentConfigurations(chainId, chainIds), salt };
    const tiered721HookConfiguration = {
      baseline721HookConfiguration: {
        name, symbol, baseUri: "ipfs://", tokenUriResolver: ZERO_ADDRESS, contractUri: "",
        tiersConfig: { tiers: [], currency: NATIVE_TOKEN_CURRENCY, decimals: 18 },
        flags: { noNewTiersWithReserves: false, noNewTiersWithVotes: false, noNewTiersWithOwnerMinting: false, preventOverspending: false },
      },
      salt,
      preventOperatorAdjustingTiers: false,
      preventOperatorUpdatingMetadata: false,
      preventOperatorMinting: false,
      preventOperatorIncreasingDiscountPercent: false,
    };
    return [0, configuration, accountingContextsToAccept, suckerDeploymentConfiguration, tiered721HookConfiguration, allowedPosts];
  }
}
