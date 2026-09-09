const permissionsContract = (chainId) => {
  switch (chainId) {
	case 1: // Ethereum Mainnet
      return "0x04fd6913d6c32d8c216e153a43c04b1857a7793d";
    case 8453: // Base
      return "0x04fd6913d6c32d8c216e153a43c04b1857a7793d";
    case 10: // Optimism
      return "0x04fd6913d6c32d8c216e153a43c04b1857a7793d";
    case 42161: // Arbitrum
      return "0x04fd6913d6c32d8c216e153a43c04b1857a7793d";
    case 11155111:
      return "0x04fd6913d6c32d8c216e153a43c04b1857a7793d";
    case 11155420:
      return "0x04fd6913d6c32d8c216e153a43c04b1857a7793d";
    case 84532:
      return "0x04fd6913d6c32d8c216e153a43c04b1857a7793d";
    case 421614:
      return "0x04fd6913d6c32d8c216e153a43c04b1857a7793d";
  }
}

const permissionsContractABI = [
	{
		"inputs": [],
		"name": "WILDCARD_PROJECT_ID",
		"outputs": [
			{
				"internalType": "uint256",
				"name": "",
				"type": "uint256"
			}
		],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [
			{
				"internalType": "address",
				"name": "operator",
				"type": "address"
			},
			{
				"internalType": "address",
				"name": "account",
				"type": "address"
			},
			{
				"internalType": "uint256",
				"name": "projectId",
				"type": "uint256"
			},
			{
				"internalType": "uint256",
				"name": "permissionId",
				"type": "uint256"
			},
			{
				"internalType": "bool",
				"name": "includeRoot",
				"type": "bool"
			},
			{
				"internalType": "bool",
				"name": "includeWildcardProjectId",
				"type": "bool"
			}
		],
		"name": "hasPermission",
		"outputs": [
			{
				"internalType": "bool",
				"name": "",
				"type": "bool"
			}
		],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [
			{
				"internalType": "address",
				"name": "operator",
				"type": "address"
			},
			{
				"internalType": "address",
				"name": "account",
				"type": "address"
			},
			{
				"internalType": "uint256",
				"name": "projectId",
				"type": "uint256"
			},
			{
				"internalType": "uint256[]",
				"name": "permissionIds",
				"type": "uint256[]"
			},
			{
				"internalType": "bool",
				"name": "includeRoot",
				"type": "bool"
			},
			{
				"internalType": "bool",
				"name": "includeWildcardProjectId",
				"type": "bool"
			}
		],
		"name": "hasPermissions",
		"outputs": [
			{
				"internalType": "bool",
				"name": "",
				"type": "bool"
			}
		],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [
			{
				"internalType": "address",
				"name": "operator",
				"type": "address"
			},
			{
				"internalType": "address",
				"name": "account",
				"type": "address"
			},
			{
				"internalType": "uint256",
				"name": "projectId",
				"type": "uint256"
			}
		],
		"name": "permissionsOf",
		"outputs": [
			{
				"internalType": "uint256",
				"name": "",
				"type": "uint256"
			}
		],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [
			{
				"internalType": "address",
				"name": "account",
				"type": "address"
			},
			{
				"components": [
					{
						"internalType": "address",
						"name": "operator",
						"type": "address"
					},
					{
						"internalType": "uint64",
						"name": "projectId",
						"type": "uint64"
					},
					{
						"internalType": "uint8[]",
						"name": "permissionIds",
						"type": "uint8[]"
					}
				],
				"internalType": "struct JBPermissionsData",
				"name": "permissionsData",
				"type": "tuple"
			}
		],
		"name": "setPermissionsFor",
		"outputs": [],
		"stateMutability": "nonpayable",
		"type": "function"
	},
	{
		"anonymous": false,
		"inputs": [
			{
				"indexed": true,
				"internalType": "address",
				"name": "operator",
				"type": "address"
			},
			{
				"indexed": true,
				"internalType": "address",
				"name": "account",
				"type": "address"
			},
			{
				"indexed": true,
				"internalType": "uint256",
				"name": "projectId",
				"type": "uint256"
			},
			{
				"indexed": false,
				"internalType": "uint8[]",
				"name": "permissionIds",
				"type": "uint8[]"
			},
			{
				"indexed": false,
				"internalType": "uint256",
				"name": "packed",
				"type": "uint256"
			},
			{
				"indexed": false,
				"internalType": "address",
				"name": "caller",
				"type": "address"
			}
		],
		"name": "OperatorPermissionsSet",
		"type": "event"
	},
	{
		"inputs": [],
		"name": "JBPermissions_CantSetRootPermissionForWildcardProject",
		"type": "error"
	},
	{
		"inputs": [],
		"name": "JBPermissions_NoZeroPermission",
		"type": "error"
	},
	{
		"inputs": [
			{
				"internalType": "uint256",
				"name": "permissionId",
				"type": "uint256"
			}
		],
		"name": "JBPermissions_PermissionIdOutOfBounds",
		"type": "error"
	},
	{
		"inputs": [],
		"name": "JBPermissions_Unauthorized",
		"type": "error"
	}
];

// Multi-chain set permissions
const tx_set_permissions = async (owner, chainId) => {
const operator = croptopPublisherContract(chainId);
const permissionsData = { operator, projectId: 0, permissionIds: [20] };
  const buildDeploymentData = async () => {
    return [owner, permissionsData];
  };

  return handleTransact(
    [chainId],
    buildDeploymentData,
    permissionsContract,
    permissionsContractABI,
    "setPermissionsFor"
  );
}
