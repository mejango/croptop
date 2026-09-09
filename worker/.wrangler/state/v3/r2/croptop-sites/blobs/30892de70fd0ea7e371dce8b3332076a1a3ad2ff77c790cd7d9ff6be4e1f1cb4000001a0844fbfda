// Read-only provider for a known chain; the browser wallet when no chain is given.
const getProvider = (chainId) => {
  const chain = chainById(chainId);
  return chain ? new ethers.JsonRpcProvider(chainRpc(chain)) : new ethers.BrowserProvider(window.ethereum);
}

// Juicebox's ERC-2771 trusted forwarder. Same address on every supported chain.
const erc2771ForwarderContract = () => "0x3ba60b60933916a7c87d0860dcee62a0ce34e3e2";

const getSigner = async (chainId) => {
  const provider = getProvider(chainId); 
  return await provider.getSigner();
}

const getChainId = async (chainId) => {
  const provider = getProvider(chainId); 
  let network = await provider.getNetwork();
  return parseInt(network.chainId);
}

const view = async (chainId, contractAddress, contractAbi, fn, params) => {
    const contract = new ethers.Contract(contractAddress, contractAbi, getProvider(chainId));
    return await contract[fn](...params);
}

const sign = async (contractAddress, contractAbi, fn, params, value) => {
    const contract = new ethers.Contract(contractAddress, contractAbi, await getSigner());
    // Let the wallet estimate gas; a fixed gasLimit made every buy display an inflated max fee.
    const tx = await contract[fn](...params, { value });
    if (!tx) return false;
    return await tx.wait();
}

// Helper to switch to a chain, adding it if needed
async function ensureWalletOnChain(chainId) {
  if (window.ethereum && window.ethereum.networkVersion !== String(chainId)) {
    try {
      await window.ethereum.request({
        method: 'wallet_switchEthereumChain',
        params: [{ chainId: '0x' + chainId.toString(16) }],
      });
    } catch (switchError) {
      if (switchError.code === 4902) {
        const chainParams = getAddChainParams(chainId);
        await window.ethereum.request({
          method: 'wallet_addEthereumChain',
          params: [chainParams],
        });
        await window.ethereum.request({
          method: 'wallet_switchEthereumChain',
          params: [{ chainId: '0x' + chainId.toString(16) }],
        });
      } else {
        throw switchError;
      }
    }
    // // Wait a moment for MetaMask to update
    // await new Promise(r => setTimeout(r, 500));
  }
}

// Placeholder for EIP-2771 signing function
const signErc2771ForwardRequest = async (forwardRequestData, chainId) => {
  // Always get a fresh signer for the current chain
  const provider = new ethers.BrowserProvider(window.ethereum);
  const signer = await provider.getSigner();
  const address = signer.address;
  if (!address) throw new Error('No signer address');
  // 48 hours from now
  const deadline = Math.floor((Date.now() + 3600 * 48 * 1000) / 1000);
  // Get nonce from forwarder contract
  const forwarderAddress = erc2771ForwarderContract(chainId);
  const forwarder = new ethers.Contract(forwarderAddress, ERC2771_FORWARDER_ABI, signer);
  const nonce = await forwarder.nonces(address);
  // Prepare the typed data for EIP-712 signing
  const domain = {
    name: "Juicebox",
    version: "1",
    chainId: chainId.toString(),
    verifyingContract: forwarderAddress,
  };
  const types = {
    ForwardRequest: [
      { name: "from", type: "address" },
      { name: "to", type: "address" },
      { name: "value", type: "uint256" },
      { name: "gas", type: "uint256" },
      { name: "nonce", type: "uint256" },
      { name: "deadline", type: "uint48" },
      { name: "data", type: "bytes" },
    ],
  };
  const message = {
    ...forwardRequestData,
    nonce: nonce.toString(),
    deadline: deadline.toString(),
  };
  // Sign the typed data
  const signature = await signer.signTypedData(domain, types, message);
  // Encode the execute function call
  const iface = new ethers.Interface(ERC2771_FORWARDER_ABI);
  const encoded = iface.encodeFunctionData("execute", [{ ...message, signature }]);
  return encoded;
}

// Params for wallet_addEthereumChain, built from the chain table.
function getAddChainParams(chainId) {
  const chain = chainById(chainId);
  if (!chain) throw new Error('Unknown chainId: ' + chainId);
  return {
    chainId: '0x' + chain.id.toString(16),
    chainName: chain.label,
    nativeCurrency: { name: 'Ether', symbol: 'ETH', decimals: 18 },
    rpcUrls: [chain.rpc],
    blockExplorerUrls: [chain.explorer],
  };
}

// Helper function to handle both single and multi-chain deployments
// `value` is a bigint/number, or an async (chainId) => bigint when it differs per chain.
const handleTransact = async (chainIds, buildDeploymentData, contractAddress, contractABI, functionName, value, useCache = false) => {
  if (!Array.isArray(chainIds)) chainIds = [chainIds];
  const valueOn = async (chainId) => BigInt(typeof value === "function" ? await value(chainId) : (value || 0));
  if (chainIds.length === 1) {
    // Single chain deployment
    const chainId = chainIds[0];
    // Always ensure wallet is on the correct chain before signing
    await ensureWalletOnChain(chainId);
    const contract = contractAddress(chainId);
    if (!contract) return false;
    const deploymentData = await buildDeploymentData(chainId);
    const receipt = await sign(contract, contractABI, functionName, deploymentData, await valueOn(chainId));
    return receipt;
  }
  // Multi-chain deployment with Relayr
  const userAddress = (await getSigner()).address;
  const relayrTransactions = [];
  
  // Only sign if not using cache
  if (!useCache) {
    for (const chainId of chainIds) {
      // Always ensure wallet is on the correct chain before signing
      await ensureWalletOnChain(chainId);
      // Always get a fresh provider/signer for the current chain
      const contract = contractAddress(chainId);
      const deploymentData = await buildDeploymentData(chainId);
      const iface = new ethers.Interface(contractABI);
      const encodedData = iface.encodeFunctionData(functionName, deploymentData);
      const txValue = await valueOn(chainId);
      const forwardRequest = {
        from: userAddress,
        to: contract,
        value: "0x" + txValue.toString(16),
        gas: "0x" + (8000000).toString(16), // ponytail: flat 8M; the old 8M x chains exceeded L1's block gas at 4 chains
        data: encodedData,
      };
      const encoded = await signErc2771ForwardRequest(forwardRequest, chainId);
      relayrTransactions.push({
        chain: chainId,
        data: encoded,
        target: erc2771ForwarderContract(chainId),
        value: txValue.toString()
      });
    }
  }
  
  const quote = await getRelayrTxQuote(relayrTransactions, useCache);
  if (!quote) {
    throw new Error("Failed to get Relayr quote");
  }
  return quote;
}

// Cache for signed transactions to avoid re-signing on network retries
let signedTransactionsCache = null;
let cacheTimestamp = null;
const CACHE_DURATION = 5 * 60 * 1000; // 5 minutes

// Get a quote for a bundle of Relayr transactions
const getRelayrTxQuote = async (relayrTransactions, useCache = false) => {
	console.log('Getting Relayr quote...');
	console.log(relayrTransactions);
	
	// Check if we can use cached transactions
	if (useCache && signedTransactionsCache && cacheTimestamp) {
		const now = Date.now();
		if (now - cacheTimestamp < CACHE_DURATION) {
			console.log('Using cached signed transactions for retry');
			relayrTransactions = signedTransactionsCache;
		} else {
			console.log('Cache expired, will need to re-sign');
			signedTransactionsCache = null;
			cacheTimestamp = null;
		}
	}
	
	// Cache the transactions if this is a fresh request
	if (!useCache) {
		signedTransactionsCache = relayrTransactions;
		cacheTimestamp = Date.now();
		console.log('Cached signed transactions for potential retry');
	}
	
  try {
    const res = await fetch('https://api.relayr.ba5ed.com/v1/bundle/prepaid', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        transactions: relayrTransactions,
        virtual_nonce_mode: "Disabled",
      }),
    });
    
    if (!res.ok) {
      throw new Error(`HTTP ${res.status}: ${res.statusText}`);
    }
    
    const result = await res.json();
    
    // Clear cache on successful request
    signedTransactionsCache = null;
    cacheTimestamp = null;
    console.log('Cleared signature cache after successful quote generation');
    
    return result;
  } catch (error) {
    console.error('Failed to get Relayr quote:', error);
    throw new Error('Network error: Unable to connect to Relayr. Please check your connection and try again.');
  }
}

// Send a Relayr transaction
const sendRelayrTx = async (paymentInfo) => {
  // paymentInfo: { target, calldata, amount, chain }
  const signer = await getSigner();
  // Switch to the correct chain if needed
  if (window.ethereum && window.ethereum.networkVersion !== String(paymentInfo.chain)) {
    if (typeof ensureWalletOnChain === 'function') {
      await ensureWalletOnChain(paymentInfo.chain);
    }
  }
  // Send the transaction
  const tx = await signer.sendTransaction({
    to: paymentInfo.target,
    data: paymentInfo.calldata,
    value: BigInt(paymentInfo.amount),
  });
  return tx;
}

const pollRelayrBundleStatus = async (bundleUuid, callback) => {
	let done = false;
	let retryCount = 0;
	const maxRetries = 10;
	
	while (!done) {
		try {
			const res = await fetch(`https://api.relayr.ba5ed.com/v1/bundle/${bundleUuid}`);
			
			if (!res.ok) {
				throw new Error(`HTTP ${res.status}: ${res.statusText}`);
			}
			
			const bundleStatus = await res.json();
			
			try {
				callback(bundleStatus);
			} catch (callbackErr) {
				// Handle callback errors silently
			}
			
			// Reset retry count on successful request
			retryCount = 0;
			
			done = bundleStatus.transactions.every(tx => tx.status && (tx.status.state === 'Success' || tx.status.state === 'Failed'));
			if (done) {
				// Make final callback with the completed state
				try {
					callback(bundleStatus);
				} catch (callbackErr) {
					// Handle callback errors silently
				}
				break; // Exit the polling loop
			} else {
				await new Promise(r => setTimeout(r, 3000));
			}
		} catch (err) {
			
			retryCount++;
			
			if (retryCount <= maxRetries) {
				// Show retry message to user
				try {
					callback({ 
						transactions: [], 
						error: `Checking deployment status... (${retryCount}/${maxRetries})`,
						retrying: true 
					});
				} catch (callbackErr) {
					console.error('Error in retry callback:', callbackErr);
				}
				
				// Wait before retry (exponential backoff)
				const retryDelay = Math.min(1000 * Math.pow(2, retryCount - 1), 10000);
				await new Promise(r => setTimeout(r, retryDelay));
			} else {
				// Max retries exceeded, show error but continue polling with longer intervals
				try {
					callback({ 
						transactions: [], 
						error: 'Checking deployment status periodically...',
						retrying: true 
					});
				} catch (callbackErr) {
					console.error('Error in max retry callback:', callbackErr);
				}
				
				// Continue polling but with longer intervals (30 seconds)
				await new Promise(r => setTimeout(r, 30000));
				retryCount = 0; // Reset retry count for next cycle
			}
		}
	}
}
