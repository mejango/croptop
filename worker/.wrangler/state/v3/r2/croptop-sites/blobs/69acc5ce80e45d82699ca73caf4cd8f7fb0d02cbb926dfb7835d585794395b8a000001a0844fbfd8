// Single source of truth for every chain this template touches.
// `setting` is the prefix of the template.json keys for that chain
// (e.g. "baseMainnet" -> baseMainnetCollectionAddress, baseMainnetRPC).
// `prefix` is the chain slug used by app.revnet.eth.sucks URLs.
const CHAINS = [
  { id: 1,        key: "ethereum mainnet", label: "Ethereum",         setting: "ethereumMainnet", prefix: "eth",  testnet: false, rpc: "https://ethereum-rpc.publicnode.com",                    explorer: "https://etherscan.io" },
  { id: 10,       key: "optimism mainnet", label: "Optimism",         setting: "optimismMainnet", prefix: "op",   testnet: false, rpc: "https://optimism-rpc.publicnode.com",               explorer: "https://optimistic.etherscan.io" },
  { id: 42161,    key: "arbitrum mainnet", label: "Arbitrum",         setting: "arbitrumMainnet", prefix: "arb",  testnet: false, rpc: "https://arbitrum-one-rpc.publicnode.com",               explorer: "https://arbiscan.io" },
  { id: 8453,     key: "base mainnet",     label: "Base",             setting: "baseMainnet",     prefix: "base", testnet: false, rpc: "https://base-rpc.publicnode.com",                   explorer: "https://basescan.org" },
  { id: 11155111, key: "ethereum sepolia", label: "Ethereum Sepolia", setting: "ethereumSepolia", prefix: "eth",  testnet: true,  rpc: "https://ethereum-sepolia-rpc.publicnode.com", explorer: "https://sepolia.etherscan.io" },
  { id: 11155420, key: "optimism sepolia", label: "Optimism Sepolia", setting: "optimismSepolia", prefix: "op",   testnet: true,  rpc: "https://sepolia.optimism.io",                 explorer: "https://sepolia-optimism.etherscan.io" },
  { id: 421614,   key: "arbitrum sepolia", label: "Arbitrum Sepolia", setting: "arbitrumSepolia", prefix: "arb",  testnet: true,  rpc: "https://arbitrum-sepolia.gateway.tenderly.co", explorer: "https://sepolia.arbiscan.io" },
  { id: 84532,    key: "base sepolia",     label: "Base Sepolia",     setting: "baseSepolia",     prefix: "base", testnet: true,  rpc: "https://sepolia.base.org",                    explorer: "https://sepolia.basescan.org" },
];

// Juicebox's native-token sentinel and its accounting currency id (uint32 of the address).
const NATIVE_TOKEN = "0x000000000000000000000000000000000000EEEe";
const NATIVE_TOKEN_CURRENCY = 61166;
// JBConstants.SPLITS_TOTAL_PERCENT: split percentages are out of this.
const SPLITS_TOTAL_PERCENT = 1000000000;

const chainById = (id) => CHAINS.find(c => c.id === Number(id));
const chainByKey = (key) => CHAINS.find(c => c.key === key);

const resolveChainId = (key) => chainByKey(key)?.id;
const resolveChain = (id) => chainById(id)?.key;
const resolveChainLabel = (id) => chainById(id)?.label;
const chainExplorerUrls = (id) => chainById(id)?.explorer + "/tx/";

// Per-chain values from templateSettings.json (the `env` global), falling back to the table.
const chainRpc = (chain) => env[chain.setting + "RPC"] || chain.rpc;
const collectionAddressFor = (chain) => (env[chain.setting + "CollectionAddress"] || "").trim();
