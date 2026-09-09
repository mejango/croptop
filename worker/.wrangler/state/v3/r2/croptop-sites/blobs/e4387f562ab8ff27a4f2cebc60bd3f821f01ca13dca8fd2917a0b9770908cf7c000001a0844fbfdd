const encodeIPFSUri = (cid) => { 
  try {
    const decodedCID = bs58.decode(cid);
    const slicedCID = decodedCID.slice(2);
    const uint8Array = new Uint8Array(slicedCID);
    let hex = '0x';
    for (let i = 0; i < uint8Array.length; i++) 
      hex += uint8Array[i].toString(16).padStart(2, '0');
    return hex;
  } catch (e) {
    return "0x0000000000000000000000000000000000000000000000000000000000000000"; 
  }
}

const encodeIPFSUriFrom = async (prefix, itemId) => {
  // Create the content's encoded IPFS URL.
  const cidUrl = `${prefix}${itemId}/nft.json.cid.txt`;
  const cid = await (await fetch(cidUrl)).text();
  return cid ? encodeIPFSUri(cid) : "0x0000000000000000000000000000000000000000000000000000000000000000";
}

const formatDate = (date) => {
  const month = date.getMonth() + 1;
  const day = date.getDate();
  const year = date.getFullYear();

  return `${month}.${day}.${year.toString().slice(-2)}`;
}

// Planet timestamps are seconds since 2001-01-01 (Apple epoch), not Unix.
const formatTimestamp = (timestamp) => formatDate(new Date((timestamp + 978307200) * 1000));

function formatEth(wei) {
  return (Number(wei) / 1e18).toFixed(8).replace(/0+$/, '').replace(/\.$/, '');
}

const formatPlayclock = (seconds) => {
  const minutes = Math.floor(seconds / 60);
  seconds = Math.floor(seconds % 60);
  return minutes + ':' + (seconds < 10 ? '0' : '') + seconds;
} 

// The Croptop network's own v6 collection (JB721TiersHook__ProjectCPN): one address on mainnets, one on testnets.
// Buys into it pay no fee; buys into any other collection pay 1/FEE_DIVISOR extra.
const CPN_COLLECTION_ADDRESSES = ["0x779957e5376571adaf544e63ffc8404e98f30d30", "0x2b70edc0b7db710f7d8708c6240d6fa9e62b71f5"];

const isCpnCollection = (address) => CPN_COLLECTION_ADDRESSES.includes((address || "").toLowerCase());

const resolveCollectionAddress = (chainKey) => collectionAddressFor(chainByKey(chainKey));

// Who receives the fee reward for buys on this site. Falls back to the buyer at the call site.
const resolveCPNBeneficiaryAddress = () => env.curatorAddress || null;

const renderMarkdown = (text) => {
  const md = window.markdownit({
    html: true,
    xhtmlOut: false,
    linkify: true
  });
  md.use(window.markdownItAnchor, { permalink: true, permalinkBefore: false, permalinkSymbol: '#' })
  .use(window.markdownitTaskLists)
  .use(window.markdownitFootnote);
  return md.render(text);
}

const fixedPointNumber = (numberString, decimals) => {
  // Find the position of the decimal point
  let decimalPos = numberString.indexOf('.');
  let fixedString;

  if (decimalPos !== -1) {
    // Split the number into whole and decimal parts
    let wholePart = numberString.substring(0, decimalPos);
    let decimalPart = numberString.substring(decimalPos + 1);
    // If the decimal part is shorter than the desired precision, pad it with zeros
    while (decimalPart.length < decimals) {
      decimalPart += '0';
    }
    // Combine the whole part and the truncated/padded decimal part
    fixedString = wholePart + decimalPart.substring(0, decimals);
  } else {
    // If there is no decimal part, just pad the number with zeros
    fixedString = numberString + '0'.repeat(decimals);
  }

  // Convert the string to a BigInt
  return BigInt(fixedString);
}

let animationIntervals = {};

/// Show a loading ticker. The frames are a CSS animation on .loading-animation::after.
const startLoadingAnimation = (id) => { document.getElementById(id).style.display = "initial"; }
const stopLoadingAnimation = (id) => { document.getElementById(id).style.display = "none"; }

let audioAnimationInterval;
const audioAnimationFrames = ['▄', '▅', '█'];

const prepareAudioAnimation = (audioAnimationId) => {
  const audioAnimation = document.getElementById(audioAnimationId);
  audioAnimation.innerHTML = audioAnimationFrames[1] + audioAnimationFrames[2] + audioAnimationFrames[0];
}
const startAudioAnimation = (audioAnimationId) => {
  const audioAnimation = document.getElementById(audioAnimationId);
  const audioAnimationPace = 100;

  const animate = () => {
    const oneOrTwo = Math.floor(Math.random() * 2) + 1;
    const char0 = audioAnimationFrames[(audioAnimationFrames.indexOf(audioAnimation.innerHTML.charAt(0)) + oneOrTwo) % 3]; 
    const char1 = audioAnimationFrames[(audioAnimationFrames.indexOf(audioAnimation.innerHTML.charAt(1)) + oneOrTwo) % 3]; 
    const char2 = audioAnimationFrames[(audioAnimationFrames.indexOf(audioAnimation.innerHTML.charAt(2)) + oneOrTwo) % 3]; 
    audioAnimation.innerHTML = char0 + char1 + char2;
    animationIntervals[audioAnimationId] = setTimeout(animate, audioAnimationPace);
  }
  animate();
}

const stopAudioAnimation = (audioAnimationId) => {
  const audioAnimation = document.getElementById(audioAnimationId);
  animationIntervals[audioAnimationId] = clearTimeout(animationIntervals[audioAnimationId]);
}

const generateSalt = () => {
  return "0x" + [...crypto.getRandomValues(new Uint8Array(32))].map(x => x.toString(16).padStart(2, '0')).join("");
}

// The project a collection (a v6 JB721TiersHook) belongs to, as a string for GraphQL. Null if unreachable.
const getProjectId = async (collectionAddress, chainId) => {
  try {
    const projectId = await view(chainId, collectionAddress, ["function projectId() view returns (uint256)"], "projectId", []);
    return projectId.toString();
  } catch (error) {
    console.error(`Error getting projectId for ${collectionAddress} on chain ${chainId}:`, error);
    return null;
  }
}

// Bendystraw project id: version-projectId-chainId. This template targets Juicebox v6.
const JB_VERSION = 6;
const idForProject = (version, projectId, chainId) => {
  return `${version}-${projectId}-${chainId}`;
}

// Generate suckerGroupId from an array of project ID strings
// Projects are sorted, joined, encoded, hashed with keccak256, and substring taken
const idForSuckerGroup = (projects) => {
  // Sort the projects array
  const sorted = projects.sort();
  
  // Join with hyphens
  const str = sorted.join("-");
  
  // Encode to Uint8Array
  const arr = new TextEncoder().encode(str);
  
  // Convert Uint8Array to hex string for ethers.keccak256
  // ethers.keccak256 expects a hex string (0x...) or BytesLike
  let hex = '0x';
  for (let i = 0; i < arr.length; i++) {
    hex += arr[i].toString(16).padStart(2, '0');
  }
  
  // Hash with keccak256 (ethers is available globally)
  const hash = ethers.keccak256(hex);
  
  // Trim leading '0x' and use next 32 characters
  const suckerGroupId = hash.substring(2, 34);
  
  return suckerGroupId;
}

// Bendystraw service URLs
const bendystrawUrl = "https://bendystraw.xyz";
const testnetBendystrawUrl = "https://testnet.bendystraw.xyz";
const BENDYSTRAW_API_KEY = "3ZNJpGtazh5fwYoSW59GWDEj";

// Get bendystraw URL based on chain
const getBendystrawUrl = (chainId) => {
  const baseUrl = chainById(chainId)?.testnet ? testnetBendystrawUrl : bendystrawUrl;
  return BENDYSTRAW_API_KEY !== "" ? `${baseUrl}/${BENDYSTRAW_API_KEY}` : baseUrl;
}

// Fetch project data from bendystraw service (to get suckerGroupId)
const fetchProjectFromBendystraw = async (projectId, chainId) => {
  try {
    
    const url = getBendystrawUrl(chainId);
    
    // GraphQL query for project data - using projects which returns items array
    // Note: projectId must be an Int, not String
    const query = `
      query GetProject($projectId: Int!, $chainId: Int!) {
        projects(where: { projectId: $projectId, chainId: $chainId, version: 6 }, limit: 1) {
          items {
            token
            tokenSymbol
            decimals
            suckerGroupId
          }
        }
      }
    `;
    
    // Convert projectId to integer (it comes as a string from getProjectId)
    const projectIdInt = parseInt(projectId, 10);
    
    const variables = { projectId: projectIdInt, chainId };
    
    const response = await fetch(`${url}/graphql`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        query,
        variables
      })
    });
    
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }
    
    const result = await response.json();
    
    if (result.errors) {
      console.error(`[fetchProjectFromBendystraw] GraphQL errors:`, result.errors);
      throw new Error(result.errors[0].message);
    }
    
    const projectData = result.data?.projects?.items?.[0] || null;
    return projectData;
  } catch (error) {
    console.error(`[fetchProjectFromBendystraw] Error fetching project ${projectId} on chain ${chainId}:`, error);
    return null;
  }
}

// Fetch balance from suckerGroup directly using suckerGroupId
const fetchBalanceFromSuckerGroup = async (suckerGroupId, chainId) => {
  try {
    const url = getBendystrawUrl(chainId);
    
    // Query suckerGroup directly using id (the schema expects 'id', not 'suckerGroupId')
    const query = `
      query GetSuckerGroupBalance($id: String!) {
        suckerGroup(id: $id) {
          balance
        }
      }
    `;
    
    const variables = { id: String(suckerGroupId) };
    
    const response = await fetch(`${url}/graphql`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        query,
        variables
      })
    });
    
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }
    
    const result = await response.json();
    
    if (result.errors) {
      console.error(`[fetchBalanceFromSuckerGroup] GraphQL errors:`, result.errors);
      throw new Error(result.errors[0].message);
    }
    
    const balance = result.data?.suckerGroup?.balance || null;
    return balance;
  } catch (error) {
    console.error(`[fetchBalanceFromSuckerGroup] Error fetching balance for suckerGroupId ${suckerGroupId}:`, error);
    return null;
  }
}

// Fetch owners count from bendystraw service
const fetchOwnersFromBendystraw = async (projectId, chainId, suckerGroupId) => {
  try {
    const url = getBendystrawUrl(chainId);
    // GraphQL query for participants (owners) - matching the pattern from the reference image
    // Note: balance_gt expects a BigInt (string) value, not an integer
    // suckerGroupId is unique and doesn't need chainId
    const query = `
      query GetParticipants($suckerGroupId: String!) {
        participants(where: { suckerGroupId: $suckerGroupId, balance_gt: "0" }, limit: 1000) {
          items {
            address
            balance
          }
        }
      }
    `;
    
    const variables = { suckerGroupId: String(suckerGroupId) };
    
    const response = await fetch(`${url}/graphql`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        query,
        variables
      })
    });
    
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }
    
    const result = await response.json();
    
    if (result.errors) {
      console.error(`[fetchOwnersFromBendystraw] GraphQL errors:`, result.errors);
      throw new Error(result.errors[0].message);
    }
    
    // Access participants - try both possible response structures
    const participantsData = result.data?.participants?.items || result.data?.participants?.participants?.items || [];
    
    // De-duplicate participants by address (same wallet on multiple chains)
    // This matches the pattern: de-dupe participants who are on multiple chains
    const participantWallets = participantsData.reduce((acc, curr) => {
      return acc.includes(curr.address) ? acc : [...acc, curr.address];
    }, []) || [];
    
    
    return participantWallets;
  } catch (error) {
    console.error(`[fetchOwnersFromBendystraw] Error fetching owners for project ${projectId} on chain ${chainId}:`, error);
    return [];
  }
}

// Fetch permission holders (operators) from bendystraw service
// Returns an object with revnetOperator and/or firstOperator
const fetchPermissionHolders = async (projectId, chainId) => {
  try {
    const url = getBendystrawUrl(chainId);
    
    // GraphQL query for permissionHolders - matching the pattern from the reference image
    const query = `
      query GetPermissionHolders($projectId: Int!, $chainId: Int!) {
        permissionHolders(where: { projectId: $projectId, chainId: $chainId, version: 6 }) {
          items {
            operator
            isRevnetOperator
            chainId
            projectId
          }
        }
      }
    `;
    
    // Convert projectId to integer
    const projectIdInt = parseInt(projectId, 10);
    const variables = { projectId: projectIdInt, chainId };
    
    const response = await fetch(`${url}/graphql`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        query,
        variables
      })
    });
    
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }
    
    const result = await response.json();
    
    if (result.errors) {
      console.error(`[fetchPermissionHolders] GraphQL errors:`, result.errors);
      throw new Error(result.errors[0].message);
    }
    
    const permissionHolders = result.data?.permissionHolders?.items || [];
    
    // Return all operators, with revnetOperator prioritized if it exists
    const allOperators = permissionHolders.map(ph => ph.operator);
    const revnetOperator = permissionHolders.find(ph => ph.isRevnetOperator === true);
    const firstOperator = permissionHolders.length > 0 ? permissionHolders[0].operator : null;
    
    const result_obj = {
      allOperators: allOperators, // All operator addresses from this chain
      revnetOperator: revnetOperator ? revnetOperator.operator : null,
      firstOperator: firstOperator
    };
    
    return result_obj;
  } catch (error) {
    console.error(`[fetchPermissionHolders] Error fetching permission holders for project ${projectId} on chain ${chainId}:`, error);
    return { allOperators: [], revnetOperator: null, firstOperator: null };
  }
}

// Every chain that has a collection address configured in templateSettings.json.
const getAllCollectionAddresses = () =>
  CHAINS.filter(collectionAddressFor).map(c => ({ chainId: c.id, collectionAddress: collectionAddressFor(c) }));

// Aggregate balance and owners across all chains. Call after loadSettings() has resolved.
const aggregateProjectData = async () => {

  const collections = getAllCollectionAddresses();
  
  if (collections.length === 0) {
    return { balance: null, owners: 0 };
  }
  
  // Get project IDs for all collections in parallel
  const projectIdPromises = collections.map(collection => {
    return getProjectId(collection.collectionAddress, collection.chainId).then(projectId => ({
      projectId,
      chainId: collection.chainId,
      collectionAddress: collection.collectionAddress
    }));
  });
  
  const projectIdResults = await Promise.all(projectIdPromises);
  
  // Filter out null projectIds
  const projects = projectIdResults.filter(result => result.projectId !== null);
  
  if (projects.length === 0) {
    return { balance: null, owners: 0 };
  }
  
  // Fetch project data to get token info and suckerGroupId
  const projectDataPromises = projects.map(p => {
    return fetchProjectFromBendystraw(p.projectId, p.chainId);
  });
  const projectDataArray = await Promise.all(projectDataPromises);
  
  // Get token info from first project (assuming all projects use same token)
  let tokenInfo = null;
  for (const projectData of projectDataArray) {
    if (projectData && projectData.token) {
      tokenInfo = {
        token: projectData.token,
        symbol: projectData.tokenSymbol || 'ETH',
        decimals: projectData.decimals || 18
      };
      break;
    }
  }
  
  // Calculate suckerGroupId from all projects (format: version-projectId-chainId).
  const projectIdStrings = projects.map(p => idForProject(JB_VERSION, parseInt(p.projectId, 10), p.chainId));
  
  const suckerGroupId = idForSuckerGroup(projectIdStrings);
  
  // Fetch balance, owners, and operators in parallel since they don't depend on each other
  const firstChainId = projects[0]?.chainId || 1;
  
  // Create all promises in parallel
  const balancePromise = fetchBalanceFromSuckerGroup(suckerGroupId, firstChainId);
  const ownersPromise = fetchOwnersFromBendystraw(null, firstChainId, suckerGroupId);
  const operatorPromises = projects.map(p => {
    return fetchPermissionHolders(p.projectId, p.chainId);
  });
  
  // Wait for all in parallel
  const [balance, ownerAddresses, ...operatorResults] = await Promise.all([
    balancePromise,
    ownersPromise,
    ...operatorPromises
  ]);
  
  
  // Extract all operators from each chain
  const allOperatorSets = operatorResults.map(or => {
    // Create a set of all operators from this chain (normalized to lowercase)
    const operators = new Set(or.allOperators.map(op => op.toLowerCase()));
    return operators;
  });
  
  
  // Also track revnetOperators and firstOperators for prioritization
  const revnetOperators = operatorResults.map(or => or.revnetOperator).filter(op => op !== null);
  const firstOperators = operatorResults.map(or => or.firstOperator).filter(op => op !== null);
  
  
  let operator = "(n/a)";
  
  // Find the intersection - operators that appear in ALL chains
  if (allOperatorSets.length > 0) {
    // Start with the first set and intersect with all others
    let commonOperators = allOperatorSets[0];
    for (let i = 1; i < allOperatorSets.length; i++) {
      const intersection = new Set();
      for (const op of commonOperators) {
        if (allOperatorSets[i].has(op)) {
          intersection.add(op);
        }
      }
      commonOperators = intersection;
    }
    
    
    if (commonOperators.size > 0) {
      // We have at least one common operator across all chains
      // Prioritize revnetOperator if it's in the common set
      const normalizedRevnetOps = revnetOperators.map(op => op.toLowerCase());
      const commonRevnet = normalizedRevnetOps.find(rop => commonOperators.has(rop));
      
      if (commonRevnet) {
        // Find the original case from revnetOperators
        operator = revnetOperators[normalizedRevnetOps.indexOf(commonRevnet)];
      } else {
        // Use the first common operator (in original case)
        // Get original case from first chain that has it
        const firstCommonOp = Array.from(commonOperators)[0];
        for (const result of operatorResults) {
          const matchingOp = result.allOperators.find(op => op.toLowerCase() === firstCommonOp);
          if (matchingOp) {
            operator = matchingOp;
            break;
          }
        }
      }
    } else {
    }
  }
  
  if (operator === "(n/a)") {
  }
  
  // Format operator address: try ENS lookup, fallback to abbreviated address
  let formattedOperator = operator;
  if (operator !== "(n/a)" && operator.startsWith("0x")) {
    try {
      formattedOperator = await formatOperatorAddress(operator);
    } catch (error) {
      console.error(`[aggregateProjectData] Error formatting operator address:`, error);
      // Fallback to abbreviated address
      formattedOperator = abbreviateAddress(operator);
    }
  }
  
  // Convert balance to BigInt for processing
  const totalBalance = balance ? BigInt(balance) : BigInt(0);
  
  // Owners are already fetched and de-duplicated, just get the count
  const totalOwners = ownerAddresses.length;
  
  // Format balance
  let formattedBalance = "$0"; // Default to $0 if no balance
  if (tokenInfo && balance && totalBalance > 0n) {
    const divisor = BigInt(10 ** tokenInfo.decimals);
    const balanceNumber = Number(totalBalance) / Number(divisor);
    
    if (tokenInfo.symbol === 'ETH') {
      // Fetch ETH price and convert to USD
      try {
        const ethPriceResponse = await fetch('https://juicebox.money/api/juicebox/prices/ethusd');
        const ethPriceData = await ethPriceResponse.json();
        const ethPrice = ethPriceData.price;
        const usdValue = balanceNumber * ethPrice;
        formattedBalance = `$${usdValue.toLocaleString('en-US', { minimumFractionDigits: 0, maximumFractionDigits: 0 })}`;
      } catch (error) {
        console.error(`[aggregateProjectData] Error fetching ETH price:`, error);
        formattedBalance = `${balanceNumber.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 6 })} ${tokenInfo.symbol}`;
      }
    } else if (tokenInfo.symbol.toLowerCase().includes('usd') || tokenInfo.symbol === 'USDC' || tokenInfo.symbol === 'USDT' || tokenInfo.symbol === 'DAI') {
      formattedBalance = `$${balanceNumber.toLocaleString('en-US', { minimumFractionDigits: 0, maximumFractionDigits: 0 })}`;
    } else {
      formattedBalance = `${balanceNumber.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 6 })} ${tokenInfo.symbol}`;
    }
  } else {
  }
  
  // Link to revnet.money on the first mainnet the project lives on (priority: eth > base > arb > op).
  const chainPriority = [1, 8453, 42161, 10];
  const preferredChainId = chainPriority.find(id => projects.some(p => p.chainId === id));
  const preferredProject = preferredChainId && projects.find(p => p.chainId === preferredChainId);
  const revnetUrl = preferredProject
    ? `https://revnet.money/${chainById(preferredChainId).prefix}:${preferredProject.projectId}`
    : null;
  
  const result = {
    balance: formattedBalance,
    owners: totalOwners,
    operator: formattedOperator,
    revnetUrl: revnetUrl
  };
  return result;
}

// Abbreviate Ethereum address to 0x0000...0000 format
const abbreviateAddress = (address) => {
  if (!address || !address.startsWith("0x") || address.length < 10) {
    return address;
  }
  return `${address.slice(0, 6)}...${address.slice(-4)}`;
}

// Resolve ENS name for an Ethereum address
// Returns the ENS name if found, otherwise returns null
const resolveENSName = async (address) => {
  try {
    // ENS is only on Ethereum mainnet (chainId 1)
    // Use a public RPC endpoint for ENS resolution
    const provider = getProvider(1);
    
    // Reverse lookup: resolve address to ENS name
    const ensName = await provider.lookupAddress(address);
    
    if (ensName) {
      // Verify the resolution (forward lookup) to prevent spoofing
      const verifiedAddress = await provider.resolveName(ensName);
      if (verifiedAddress?.toLowerCase() === address.toLowerCase()) {
        return ensName;
      }
    }
    
    return null;
  } catch (error) {
    console.error(`[resolveENSName] Error resolving ENS for ${address}:`, error);
    return null;
  }
}

// Format operator address: try ENS lookup, fallback to abbreviated address
const formatOperatorAddress = async (address) => {
  if (!address || !address.startsWith("0x")) {
    return address;
  }
  
  const ensName = await resolveENSName(address);
  if (ensName) {
    return ensName;
  }
  
  return abbreviateAddress(address);
}

