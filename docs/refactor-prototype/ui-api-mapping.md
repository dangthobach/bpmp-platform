# UI control/event → API traceability

Generated lexical candidates, not verified semantic coverage. Every inventory row is included once; a control and its callback can represent the same interaction. All rows remain pending source/browser review. Do not count API_CANDIDATE as implemented or accepted parity.

Full source hash/expression: [inventory JSON](interaction-inventory.json). Machine mapping: [CSV](ui-api-mapping.csv). Operation IDs: [API reference](api-operation-reference.md).

| Classification | Rows |
|---|---:|
| API_CANDIDATE | 195 |
| LOCAL_DRAFT | 313 |
| LOCAL_UI | 805 |
| NO_DIRECT_HANDLER | 13 |

## Mapping

| UI ID | File:line | Event/control | Classification | Operation IDs |
|---|---|---|---|---|
| UI-0001 | App.tsx:123 | onSelectModule | LOCAL_UI |  |
| UI-0002 | App.tsx:132 | onToggleCollapse | LOCAL_UI |  |
| UI-0003 | App.tsx:134 | onOpenEndpointsModal | LOCAL_UI |  |
| UI-0004 | App.tsx:136 | onSelectWorkspace | LOCAL_UI |  |
| UI-0005 | App.tsx:146 | onToggleDarkMode | LOCAL_UI |  |
| UI-0006 | App.tsx:148 | onSelectTenant | API_CANDIDATE | targetSwitchSessionTenant |
| UI-0007 | App.tsx:153 | onSelectWorkspace | LOCAL_UI |  |
| UI-0008 | App.tsx:160 | onNotify | LOCAL_UI |  |
| UI-0009 | App.tsx:161 | onOpenAdminConsole | LOCAL_UI |  |
| UI-0010 | App.tsx:163 | onProcessInstanceCreated | API_CANDIDATE | targetCreateCustomerCase;targetSubmitCustomerCase |
| UI-0011 | App.tsx:173 | onUpdateProcess | API_CANDIDATE | targetSaveProcessModelDraft |
| UI-0012 | App.tsx:175 | onSelectProcess | LOCAL_UI |  |
| UI-0013 | App.tsx:176 | onDeployToEngine | API_CANDIDATE | targetCreateDeployment |
| UI-0014 | App.tsx:184 | onUpdateInstances | API_CANDIDATE | targetBatchInstanceActions |
| UI-0015 | App.tsx:185 | onSelectProcess | LOCAL_UI |  |
| UI-0016 | App.tsx:207 | onTaskCompleted | API_CANDIDATE | completeWorkItem;targetSubmitWorkItemForm |
| UI-0017 | App.tsx:213 | onRegisterCustomWorkflow | API_CANDIDATE | targetCreateProcessModel;targetInstantiateTemplate |
| UI-0018 | App.tsx:214 | onOpenEndpointsModal | LOCAL_UI |  |
| UI-0019 | App.tsx:215 | onNavigateToModeler | LOCAL_UI |  |
| UI-0020 | App.tsx:229 | onClose | LOCAL_UI |  |
| UI-0021 | components/common/Header.tsx:123 | onClick | LOCAL_UI |  |
| UI-0022 | components/common/Header.tsx:141 | button | LOCAL_UI |  |
| UI-0023 | components/common/Header.tsx:143 | onClick | LOCAL_UI |  |
| UI-0024 | components/common/Header.tsx:169 | button | LOCAL_UI |  |
| UI-0025 | components/common/Header.tsx:170 | onClick | LOCAL_UI |  |
| UI-0026 | components/common/Header.tsx:181 | button | LOCAL_UI |  |
| UI-0027 | components/common/Header.tsx:182 | onClick | LOCAL_UI |  |
| UI-0028 | components/common/Header.tsx:199 | input | NO_DIRECT_HANDLER |  |
| UI-0029 | components/common/Header.tsx:211 | button | LOCAL_UI |  |
| UI-0030 | components/common/Header.tsx:212 | onClick | LOCAL_UI |  |
| UI-0031 | components/common/Header.tsx:242 | button | NO_DIRECT_HANDLER |  |
| UI-0032 | components/common/Header.tsx:251 | button | LOCAL_UI |  |
| UI-0033 | components/common/Header.tsx:252 | onClick | LOCAL_UI |  |
| UI-0034 | components/common/Header.tsx:263 | button | LOCAL_UI |  |
| UI-0035 | components/common/Header.tsx:264 | onClick | LOCAL_UI |  |
| UI-0036 | components/common/Header.tsx:299 | button | LOCAL_UI |  |
| UI-0037 | components/common/Header.tsx:300 | onClick | LOCAL_UI |  |
| UI-0038 | components/common/Sidebar.tsx:83 | button | LOCAL_UI |  |
| UI-0039 | components/common/Sidebar.tsx:85 | onClick | LOCAL_UI |  |
| UI-0040 | components/common/Sidebar.tsx:116 | button | LOCAL_UI |  |
| UI-0041 | components/common/Sidebar.tsx:117 | onClick | LOCAL_UI |  |
| UI-0042 | components/common/Sidebar.tsx:138 | button | LOCAL_UI |  |
| UI-0043 | components/common/Sidebar.tsx:139 | onClick | LOCAL_UI |  |
| UI-0044 | components/foundation/EndpointsModal.tsx:92 | button | LOCAL_UI |  |
| UI-0045 | components/foundation/EndpointsModal.tsx:92 | onClick | LOCAL_UI |  |
| UI-0046 | components/foundation/EndpointsModal.tsx:100 | button | LOCAL_UI |  |
| UI-0047 | components/foundation/EndpointsModal.tsx:101 | onClick | LOCAL_UI |  |
| UI-0048 | components/foundation/EndpointsModal.tsx:156 | button | LOCAL_UI |  |
| UI-0049 | components/foundation/EndpointsModal.tsx:157 | onClick | LOCAL_UI |  |
| UI-0050 | components/foundation/EndpointsModal.tsx:163 | button | API_CANDIDATE | targetSoftDeleteEndpoint |
| UI-0051 | components/foundation/EndpointsModal.tsx:164 | onClick | API_CANDIDATE | targetSoftDeleteEndpoint |
| UI-0052 | components/foundation/EndpointsModal.tsx:185 | button | NO_DIRECT_HANDLER |  |
| UI-0053 | components/foundation/EndpointsModal.tsx:191 | button | NO_DIRECT_HANDLER |  |
| UI-0054 | components/foundation/EndpointsModal.tsx:198 | select | NO_DIRECT_HANDLER |  |
| UI-0055 | components/foundation/EndpointsModal.tsx:209 | button | LOCAL_UI |  |
| UI-0056 | components/foundation/EndpointsModal.tsx:210 | onClick | LOCAL_UI |  |
| UI-0057 | components/foundation/EndpointsModal.tsx:226 | button | LOCAL_UI |  |
| UI-0058 | components/foundation/EndpointsModal.tsx:226 | onClick | LOCAL_UI |  |
| UI-0059 | components/foundation/EndpointsModal.tsx:231 | input | LOCAL_DRAFT |  |
| UI-0060 | components/foundation/EndpointsModal.tsx:234 | onChange | LOCAL_DRAFT |  |
| UI-0061 | components/foundation/EndpointsModal.tsx:241 | select | LOCAL_DRAFT |  |
| UI-0062 | components/foundation/EndpointsModal.tsx:243 | onChange | LOCAL_DRAFT |  |
| UI-0063 | components/foundation/EndpointsModal.tsx:253 | input | LOCAL_DRAFT |  |
| UI-0064 | components/foundation/EndpointsModal.tsx:256 | onChange | LOCAL_DRAFT |  |
| UI-0065 | components/foundation/EndpointsModal.tsx:264 | select | LOCAL_DRAFT |  |
| UI-0066 | components/foundation/EndpointsModal.tsx:266 | onChange | LOCAL_DRAFT |  |
| UI-0067 | components/foundation/EndpointsModal.tsx:277 | select | LOCAL_DRAFT |  |
| UI-0068 | components/foundation/EndpointsModal.tsx:279 | onChange | LOCAL_DRAFT |  |
| UI-0069 | components/foundation/EndpointsModal.tsx:291 | button | LOCAL_UI |  |
| UI-0070 | components/foundation/EndpointsModal.tsx:291 | onClick | LOCAL_UI |  |
| UI-0071 | components/foundation/EndpointsModal.tsx:294 | button | API_CANDIDATE | targetCreateEndpoint;targetUpdateEndpoint |
| UI-0072 | components/foundation/EndpointsModal.tsx:294 | onClick | API_CANDIDATE | targetCreateEndpoint;targetUpdateEndpoint |
| UI-0073 | components/foundation/FoundationModule.tsx:145 | button | LOCAL_UI |  |
| UI-0074 | components/foundation/FoundationModule.tsx:146 | onClick | LOCAL_UI |  |
| UI-0075 | components/foundation/FoundationModule.tsx:156 | button | LOCAL_UI |  |
| UI-0076 | components/foundation/FoundationModule.tsx:157 | onClick | LOCAL_UI |  |
| UI-0077 | components/foundation/FoundationModule.tsx:167 | button | LOCAL_UI |  |
| UI-0078 | components/foundation/FoundationModule.tsx:168 | onClick | LOCAL_UI |  |
| UI-0079 | components/foundation/FoundationModule.tsx:178 | button | LOCAL_UI |  |
| UI-0080 | components/foundation/FoundationModule.tsx:179 | onClick | LOCAL_UI |  |
| UI-0081 | components/foundation/FoundationModule.tsx:190 | button | LOCAL_UI |  |
| UI-0082 | components/foundation/FoundationModule.tsx:191 | onClick | LOCAL_UI |  |
| UI-0083 | components/foundation/FoundationModule.tsx:209 | button | LOCAL_UI |  |
| UI-0084 | components/foundation/FoundationModule.tsx:210 | onClick | LOCAL_UI |  |
| UI-0085 | components/foundation/FoundationModule.tsx:236 | button | LOCAL_UI |  |
| UI-0086 | components/foundation/FoundationModule.tsx:239 | onClick | LOCAL_UI |  |
| UI-0087 | components/foundation/FoundationModule.tsx:257 | input | LOCAL_DRAFT |  |
| UI-0088 | components/foundation/FoundationModule.tsx:260 | onChange | LOCAL_DRAFT |  |
| UI-0089 | components/foundation/FoundationModule.tsx:267 | select | LOCAL_UI |  |
| UI-0090 | components/foundation/FoundationModule.tsx:269 | onChange | LOCAL_DRAFT |  |
| UI-0091 | components/foundation/FoundationModule.tsx:279 | input | LOCAL_DRAFT |  |
| UI-0092 | components/foundation/FoundationModule.tsx:282 | onChange | LOCAL_DRAFT |  |
| UI-0093 | components/foundation/FoundationModule.tsx:294 | button | LOCAL_UI |  |
| UI-0094 | components/foundation/FoundationModule.tsx:296 | onClick | LOCAL_UI |  |
| UI-0095 | components/foundation/FoundationModule.tsx:310 | input | LOCAL_UI |  |
| UI-0096 | components/foundation/FoundationModule.tsx:313 | onChange | LOCAL_DRAFT |  |
| UI-0097 | components/foundation/FoundationModule.tsx:321 | button | LOCAL_UI |  |
| UI-0098 | components/foundation/FoundationModule.tsx:323 | onClick | LOCAL_UI |  |
| UI-0099 | components/foundation/FoundationModule.tsx:336 | button | API_CANDIDATE | targetCreateProcessModel;targetInstantiateTemplate |
| UI-0100 | components/foundation/FoundationModule.tsx:338 | onClick | API_CANDIDATE | targetCreateProcessModel;targetInstantiateTemplate |
| UI-0101 | components/foundation/FoundationModule.tsx:378 | button | API_CANDIDATE | targetUpdateAssignmentPolicy |
| UI-0102 | components/foundation/FoundationModule.tsx:379 | onClick | API_CANDIDATE | targetUpdateAssignmentPolicy |
| UI-0103 | components/identity/IdentityModule.tsx:809 | button | LOCAL_UI |  |
| UI-0104 | components/identity/IdentityModule.tsx:810 | onClick | LOCAL_UI |  |
| UI-0105 | components/identity/IdentityModule.tsx:818 | button | LOCAL_UI |  |
| UI-0106 | components/identity/IdentityModule.tsx:819 | onClick | LOCAL_UI |  |
| UI-0107 | components/identity/IdentityModule.tsx:849 | button | LOCAL_UI |  |
| UI-0108 | components/identity/IdentityModule.tsx:850 | onClick | LOCAL_UI |  |
| UI-0109 | components/identity/IdentityModule.tsx:861 | button | LOCAL_UI |  |
| UI-0110 | components/identity/IdentityModule.tsx:862 | onClick | LOCAL_UI |  |
| UI-0111 | components/identity/IdentityModule.tsx:873 | button | LOCAL_UI |  |
| UI-0112 | components/identity/IdentityModule.tsx:874 | onClick | LOCAL_UI |  |
| UI-0113 | components/identity/IdentityModule.tsx:888 | button | LOCAL_UI |  |
| UI-0114 | components/identity/IdentityModule.tsx:889 | onClick | LOCAL_UI |  |
| UI-0115 | components/identity/IdentityModule.tsx:903 | button | LOCAL_UI |  |
| UI-0116 | components/identity/IdentityModule.tsx:904 | onClick | LOCAL_UI |  |
| UI-0117 | components/identity/IdentityModule.tsx:918 | button | LOCAL_UI |  |
| UI-0118 | components/identity/IdentityModule.tsx:919 | onClick | LOCAL_UI |  |
| UI-0119 | components/identity/IdentityModule.tsx:933 | button | LOCAL_UI |  |
| UI-0120 | components/identity/IdentityModule.tsx:934 | onClick | LOCAL_UI |  |
| UI-0121 | components/identity/IdentityModule.tsx:958 | onClick | LOCAL_UI |  |
| UI-0122 | components/identity/IdentityModule.tsx:971 | onClick | LOCAL_UI |  |
| UI-0123 | components/identity/IdentityModule.tsx:984 | onClick | LOCAL_UI |  |
| UI-0124 | components/identity/IdentityModule.tsx:997 | onClick | LOCAL_UI |  |
| UI-0125 | components/identity/IdentityModule.tsx:1013 | onClick | LOCAL_UI |  |
| UI-0126 | components/identity/IdentityModule.tsx:1026 | onClick | LOCAL_UI |  |
| UI-0127 | components/identity/IdentityModule.tsx:1039 | onClick | LOCAL_UI |  |
| UI-0128 | components/identity/IdentityModule.tsx:1060 | button | LOCAL_UI |  |
| UI-0129 | components/identity/IdentityModule.tsx:1061 | onClick | LOCAL_UI |  |
| UI-0130 | components/identity/IdentityModule.tsx:1067 | button | LOCAL_UI |  |
| UI-0131 | components/identity/IdentityModule.tsx:1068 | onClick | LOCAL_UI |  |
| UI-0132 | components/identity/IdentityModule.tsx:1074 | button | LOCAL_UI |  |
| UI-0133 | components/identity/IdentityModule.tsx:1075 | onClick | LOCAL_UI |  |
| UI-0134 | components/identity/IdentityModule.tsx:1081 | button | LOCAL_UI |  |
| UI-0135 | components/identity/IdentityModule.tsx:1082 | onClick | LOCAL_UI |  |
| UI-0136 | components/identity/IdentityModule.tsx:1135 | button | API_CANDIDATE | targetBatchUserActions |
| UI-0137 | components/identity/IdentityModule.tsx:1136 | onClick | API_CANDIDATE | targetBatchUserActions |
| UI-0138 | components/identity/IdentityModule.tsx:1144 | button | LOCAL_UI |  |
| UI-0139 | components/identity/IdentityModule.tsx:1145 | onClick | LOCAL_UI |  |
| UI-0140 | components/identity/IdentityModule.tsx:1173 | button | LOCAL_UI |  |
| UI-0141 | components/identity/IdentityModule.tsx:1174 | onClick | LOCAL_UI |  |
| UI-0142 | components/identity/IdentityModule.tsx:1182 | button | LOCAL_UI |  |
| UI-0143 | components/identity/IdentityModule.tsx:1183 | onClick | LOCAL_UI |  |
| UI-0144 | components/identity/IdentityModule.tsx:1191 | button | LOCAL_UI |  |
| UI-0145 | components/identity/IdentityModule.tsx:1192 | onClick | LOCAL_UI |  |
| UI-0146 | components/identity/IdentityModule.tsx:1200 | button | API_CANDIDATE | targetBatchUserActions |
| UI-0147 | components/identity/IdentityModule.tsx:1201 | onClick | API_CANDIDATE | targetBatchUserActions |
| UI-0148 | components/identity/IdentityModule.tsx:1208 | button | API_CANDIDATE | targetBatchUserActions |
| UI-0149 | components/identity/IdentityModule.tsx:1209 | onClick | API_CANDIDATE | targetBatchUserActions |
| UI-0150 | components/identity/IdentityModule.tsx:1216 | button | LOCAL_UI |  |
| UI-0151 | components/identity/IdentityModule.tsx:1217 | onClick | LOCAL_UI |  |
| UI-0152 | components/identity/IdentityModule.tsx:1224 | button | LOCAL_UI |  |
| UI-0153 | components/identity/IdentityModule.tsx:1225 | onClick | LOCAL_UI |  |
| UI-0154 | components/identity/IdentityModule.tsx:1232 | button | LOCAL_UI |  |
| UI-0155 | components/identity/IdentityModule.tsx:1233 | onClick | LOCAL_UI |  |
| UI-0156 | components/identity/IdentityModule.tsx:1247 | input | LOCAL_UI |  |
| UI-0157 | components/identity/IdentityModule.tsx:1251 | onChange | LOCAL_DRAFT |  |
| UI-0158 | components/identity/IdentityModule.tsx:1256 | button | LOCAL_UI |  |
| UI-0159 | components/identity/IdentityModule.tsx:1257 | onClick | LOCAL_UI |  |
| UI-0160 | components/identity/IdentityModule.tsx:1270 | button | LOCAL_UI |  |
| UI-0161 | components/identity/IdentityModule.tsx:1271 | onClick | LOCAL_UI |  |
| UI-0162 | components/identity/IdentityModule.tsx:1278 | button | LOCAL_UI |  |
| UI-0163 | components/identity/IdentityModule.tsx:1279 | onClick | LOCAL_UI |  |
| UI-0164 | components/identity/IdentityModule.tsx:1286 | button | LOCAL_UI |  |
| UI-0165 | components/identity/IdentityModule.tsx:1287 | onClick | LOCAL_UI |  |
| UI-0166 | components/identity/IdentityModule.tsx:1307 | button | LOCAL_UI |  |
| UI-0167 | components/identity/IdentityModule.tsx:1309 | onClick | LOCAL_UI |  |
| UI-0168 | components/identity/IdentityModule.tsx:1327 | button | LOCAL_UI |  |
| UI-0169 | components/identity/IdentityModule.tsx:1329 | onClick | LOCAL_UI |  |
| UI-0170 | components/identity/IdentityModule.tsx:1344 | button | LOCAL_UI |  |
| UI-0171 | components/identity/IdentityModule.tsx:1346 | onClick | LOCAL_UI |  |
| UI-0172 | components/identity/IdentityModule.tsx:1358 | button | LOCAL_UI |  |
| UI-0173 | components/identity/IdentityModule.tsx:1360 | onClick | LOCAL_UI |  |
| UI-0174 | components/identity/IdentityModule.tsx:1367 | button | LOCAL_UI |  |
| UI-0175 | components/identity/IdentityModule.tsx:1369 | onClick | LOCAL_UI |  |
| UI-0176 | components/identity/IdentityModule.tsx:1378 | button | LOCAL_UI |  |
| UI-0177 | components/identity/IdentityModule.tsx:1380 | onClick | LOCAL_UI |  |
| UI-0178 | components/identity/IdentityModule.tsx:1385 | button | LOCAL_UI |  |
| UI-0179 | components/identity/IdentityModule.tsx:1387 | onClick | LOCAL_UI |  |
| UI-0180 | components/identity/IdentityModule.tsx:1426 | button | LOCAL_UI |  |
| UI-0181 | components/identity/IdentityModule.tsx:1428 | onClick | LOCAL_UI |  |
| UI-0182 | components/identity/IdentityModule.tsx:1508 | button | API_CANDIDATE | targetLockUser;targetUnlockUser |
| UI-0183 | components/identity/IdentityModule.tsx:1509 | onClick | API_CANDIDATE | targetLockUser;targetUnlockUser |
| UI-0184 | components/identity/IdentityModule.tsx:1552 | button | API_CANDIDATE | targetBatchRoleActions |
| UI-0185 | components/identity/IdentityModule.tsx:1553 | onClick | API_CANDIDATE | targetBatchRoleActions |
| UI-0186 | components/identity/IdentityModule.tsx:1582 | button | LOCAL_UI |  |
| UI-0187 | components/identity/IdentityModule.tsx:1583 | onClick | LOCAL_UI |  |
| UI-0188 | components/identity/IdentityModule.tsx:1596 | button | LOCAL_UI |  |
| UI-0189 | components/identity/IdentityModule.tsx:1597 | onClick | LOCAL_UI |  |
| UI-0190 | components/identity/IdentityModule.tsx:1606 | button | LOCAL_UI |  |
| UI-0191 | components/identity/IdentityModule.tsx:1607 | onClick | LOCAL_UI |  |
| UI-0192 | components/identity/IdentityModule.tsx:1616 | button | API_CANDIDATE | targetBatchRoleActions |
| UI-0193 | components/identity/IdentityModule.tsx:1617 | onClick | API_CANDIDATE | targetBatchRoleActions |
| UI-0194 | components/identity/IdentityModule.tsx:1626 | button | LOCAL_UI |  |
| UI-0195 | components/identity/IdentityModule.tsx:1627 | onClick | LOCAL_UI |  |
| UI-0196 | components/identity/IdentityModule.tsx:1636 | button | LOCAL_UI |  |
| UI-0197 | components/identity/IdentityModule.tsx:1637 | onClick | LOCAL_UI |  |
| UI-0198 | components/identity/IdentityModule.tsx:1650 | button | LOCAL_UI |  |
| UI-0199 | components/identity/IdentityModule.tsx:1652 | onClick | LOCAL_UI |  |
| UI-0200 | components/identity/IdentityModule.tsx:1667 | input | LOCAL_DRAFT |  |
| UI-0201 | components/identity/IdentityModule.tsx:1671 | onChange | LOCAL_DRAFT |  |
| UI-0202 | components/identity/IdentityModule.tsx:1681 | button | LOCAL_UI |  |
| UI-0203 | components/identity/IdentityModule.tsx:1683 | onClick | LOCAL_UI |  |
| UI-0204 | components/identity/IdentityModule.tsx:1694 | button | LOCAL_UI |  |
| UI-0205 | components/identity/IdentityModule.tsx:1696 | onClick | LOCAL_UI |  |
| UI-0206 | components/identity/IdentityModule.tsx:1712 | button | LOCAL_UI |  |
| UI-0207 | components/identity/IdentityModule.tsx:1714 | onClick | LOCAL_UI |  |
| UI-0208 | components/identity/IdentityModule.tsx:1736 | button | LOCAL_UI |  |
| UI-0209 | components/identity/IdentityModule.tsx:1738 | onClick | LOCAL_UI |  |
| UI-0210 | components/identity/IdentityModule.tsx:1777 | onClick | LOCAL_UI |  |
| UI-0211 | components/identity/IdentityModule.tsx:1782 | onClick | LOCAL_UI |  |
| UI-0212 | components/identity/IdentityModule.tsx:1784 | button | LOCAL_UI |  |
| UI-0213 | components/identity/IdentityModule.tsx:1786 | onClick | LOCAL_UI |  |
| UI-0214 | components/identity/IdentityModule.tsx:1821 | button | LOCAL_UI |  |
| UI-0215 | components/identity/IdentityModule.tsx:1823 | onClick | LOCAL_UI |  |
| UI-0216 | components/identity/IdentityModule.tsx:1853 | button | LOCAL_UI |  |
| UI-0217 | components/identity/IdentityModule.tsx:1855 | onClick | LOCAL_UI |  |
| UI-0218 | components/identity/IdentityModule.tsx:1869 | onClick | LOCAL_UI |  |
| UI-0219 | components/identity/IdentityModule.tsx:1872 | button | LOCAL_UI |  |
| UI-0220 | components/identity/IdentityModule.tsx:1874 | onClick | LOCAL_UI |  |
| UI-0221 | components/identity/IdentityModule.tsx:1885 | button | LOCAL_UI |  |
| UI-0222 | components/identity/IdentityModule.tsx:1887 | onClick | LOCAL_UI |  |
| UI-0223 | components/identity/IdentityModule.tsx:1897 | button | LOCAL_UI |  |
| UI-0224 | components/identity/IdentityModule.tsx:1899 | onClick | LOCAL_UI |  |
| UI-0225 | components/identity/IdentityModule.tsx:1935 | button | LOCAL_UI |  |
| UI-0226 | components/identity/IdentityModule.tsx:1937 | onClick | LOCAL_UI |  |
| UI-0227 | components/identity/IdentityModule.tsx:1959 | button | LOCAL_UI |  |
| UI-0228 | components/identity/IdentityModule.tsx:1961 | onClick | LOCAL_UI |  |
| UI-0229 | components/identity/IdentityModule.tsx:1981 | onClick | LOCAL_UI |  |
| UI-0230 | components/identity/IdentityModule.tsx:2032 | button | API_CANDIDATE | targetBatchPermissionActions |
| UI-0231 | components/identity/IdentityModule.tsx:2033 | onClick | API_CANDIDATE | targetBatchPermissionActions |
| UI-0232 | components/identity/IdentityModule.tsx:2061 | button | LOCAL_UI |  |
| UI-0233 | components/identity/IdentityModule.tsx:2062 | onClick | LOCAL_UI |  |
| UI-0234 | components/identity/IdentityModule.tsx:2070 | button | LOCAL_UI |  |
| UI-0235 | components/identity/IdentityModule.tsx:2071 | onClick | LOCAL_UI |  |
| UI-0236 | components/identity/IdentityModule.tsx:2079 | button | LOCAL_UI |  |
| UI-0237 | components/identity/IdentityModule.tsx:2080 | onClick | LOCAL_UI |  |
| UI-0238 | components/identity/IdentityModule.tsx:2092 | button | API_CANDIDATE | targetBatchPermissionActions |
| UI-0239 | components/identity/IdentityModule.tsx:2093 | onClick | API_CANDIDATE | targetBatchPermissionActions |
| UI-0240 | components/identity/IdentityModule.tsx:2101 | button | API_CANDIDATE | targetBatchPermissionActions |
| UI-0241 | components/identity/IdentityModule.tsx:2102 | onClick | API_CANDIDATE | targetBatchPermissionActions |
| UI-0242 | components/identity/IdentityModule.tsx:2109 | button | API_CANDIDATE | targetBatchPermissionActions |
| UI-0243 | components/identity/IdentityModule.tsx:2110 | onClick | API_CANDIDATE | targetBatchPermissionActions |
| UI-0244 | components/identity/IdentityModule.tsx:2117 | button | LOCAL_UI |  |
| UI-0245 | components/identity/IdentityModule.tsx:2118 | onClick | LOCAL_UI |  |
| UI-0246 | components/identity/IdentityModule.tsx:2131 | input | LOCAL_DRAFT |  |
| UI-0247 | components/identity/IdentityModule.tsx:2135 | onChange | LOCAL_DRAFT |  |
| UI-0248 | components/identity/IdentityModule.tsx:2144 | button | LOCAL_UI |  |
| UI-0249 | components/identity/IdentityModule.tsx:2146 | onClick | LOCAL_UI |  |
| UI-0250 | components/identity/IdentityModule.tsx:2166 | button | LOCAL_UI |  |
| UI-0251 | components/identity/IdentityModule.tsx:2168 | onClick | LOCAL_UI |  |
| UI-0252 | components/identity/IdentityModule.tsx:2201 | button | LOCAL_UI |  |
| UI-0253 | components/identity/IdentityModule.tsx:2203 | onClick | LOCAL_UI |  |
| UI-0254 | components/identity/IdentityModule.tsx:2225 | button | API_CANDIDATE | targetUpdatePermission |
| UI-0255 | components/identity/IdentityModule.tsx:2227 | onClick | API_CANDIDATE | targetUpdatePermission |
| UI-0256 | components/identity/IdentityModule.tsx:2285 | button | API_CANDIDATE | targetBatchResourceActions |
| UI-0257 | components/identity/IdentityModule.tsx:2286 | onClick | API_CANDIDATE | targetBatchResourceActions |
| UI-0258 | components/identity/IdentityModule.tsx:2320 | button | API_CANDIDATE | targetBatchResourceActions |
| UI-0259 | components/identity/IdentityModule.tsx:2321 | onClick | API_CANDIDATE | targetBatchResourceActions |
| UI-0260 | components/identity/IdentityModule.tsx:2326 | button | API_CANDIDATE | targetBatchResourceActions |
| UI-0261 | components/identity/IdentityModule.tsx:2327 | onClick | API_CANDIDATE | targetBatchResourceActions |
| UI-0262 | components/identity/IdentityModule.tsx:2332 | button | API_CANDIDATE | targetBatchResourceActions |
| UI-0263 | components/identity/IdentityModule.tsx:2333 | onClick | API_CANDIDATE | targetBatchResourceActions |
| UI-0264 | components/identity/IdentityModule.tsx:2338 | button | API_CANDIDATE | targetBatchResourceActions |
| UI-0265 | components/identity/IdentityModule.tsx:2339 | onClick | API_CANDIDATE | targetBatchResourceActions |
| UI-0266 | components/identity/IdentityModule.tsx:2345 | button | API_CANDIDATE | targetBatchResourceActions |
| UI-0267 | components/identity/IdentityModule.tsx:2346 | onClick | API_CANDIDATE | targetBatchResourceActions |
| UI-0268 | components/identity/IdentityModule.tsx:2351 | button | LOCAL_UI |  |
| UI-0269 | components/identity/IdentityModule.tsx:2352 | onClick | LOCAL_UI |  |
| UI-0270 | components/identity/IdentityModule.tsx:2368 | button | LOCAL_UI |  |
| UI-0271 | components/identity/IdentityModule.tsx:2370 | onClick | LOCAL_UI |  |
| UI-0272 | components/identity/IdentityModule.tsx:2403 | button | LOCAL_UI |  |
| UI-0273 | components/identity/IdentityModule.tsx:2405 | onClick | LOCAL_UI |  |
| UI-0274 | components/identity/IdentityModule.tsx:2474 | button | API_CANDIDATE | targetBatchServiceAccountActions |
| UI-0275 | components/identity/IdentityModule.tsx:2475 | onClick | API_CANDIDATE | targetBatchServiceAccountActions |
| UI-0276 | components/identity/IdentityModule.tsx:2482 | button | API_CANDIDATE | targetBatchServiceAccountActions |
| UI-0277 | components/identity/IdentityModule.tsx:2483 | onClick | API_CANDIDATE | targetBatchServiceAccountActions |
| UI-0278 | components/identity/IdentityModule.tsx:2488 | button | API_CANDIDATE | targetBatchServiceAccountActions |
| UI-0279 | components/identity/IdentityModule.tsx:2489 | onClick | API_CANDIDATE | targetBatchServiceAccountActions |
| UI-0280 | components/identity/IdentityModule.tsx:2494 | button | API_CANDIDATE | targetBatchServiceAccountActions |
| UI-0281 | components/identity/IdentityModule.tsx:2495 | onClick | API_CANDIDATE | targetBatchServiceAccountActions |
| UI-0282 | components/identity/IdentityModule.tsx:2500 | button | LOCAL_UI |  |
| UI-0283 | components/identity/IdentityModule.tsx:2501 | onClick | LOCAL_UI |  |
| UI-0284 | components/identity/IdentityModule.tsx:2517 | button | LOCAL_UI |  |
| UI-0285 | components/identity/IdentityModule.tsx:2519 | onClick | LOCAL_UI |  |
| UI-0286 | components/identity/IdentityModule.tsx:2552 | button | LOCAL_UI |  |
| UI-0287 | components/identity/IdentityModule.tsx:2554 | onClick | LOCAL_UI |  |
| UI-0288 | components/identity/IdentityModule.tsx:2675 | button | LOCAL_UI |  |
| UI-0289 | components/identity/IdentityModule.tsx:2676 | onClick | LOCAL_UI |  |
| UI-0290 | components/identity/IdentityModule.tsx:2685 | input | LOCAL_DRAFT |  |
| UI-0291 | components/identity/IdentityModule.tsx:2688 | onChange | LOCAL_DRAFT |  |
| UI-0292 | components/identity/IdentityModule.tsx:2695 | input | LOCAL_DRAFT |  |
| UI-0293 | components/identity/IdentityModule.tsx:2698 | onChange | LOCAL_DRAFT |  |
| UI-0294 | components/identity/IdentityModule.tsx:2705 | input | LOCAL_DRAFT |  |
| UI-0295 | components/identity/IdentityModule.tsx:2708 | onChange | LOCAL_DRAFT |  |
| UI-0296 | components/identity/IdentityModule.tsx:2715 | select | LOCAL_DRAFT |  |
| UI-0297 | components/identity/IdentityModule.tsx:2717 | onChange | LOCAL_DRAFT |  |
| UI-0298 | components/identity/IdentityModule.tsx:2730 | button | LOCAL_UI |  |
| UI-0299 | components/identity/IdentityModule.tsx:2731 | onClick | LOCAL_UI |  |
| UI-0300 | components/identity/IdentityModule.tsx:2736 | button | API_CANDIDATE | targetCreateUser |
| UI-0301 | components/identity/IdentityModule.tsx:2737 | onClick | API_CANDIDATE | targetCreateUser |
| UI-0302 | components/identity/IdentityModule.tsx:2765 | button | API_CANDIDATE | targetBatchUserActions |
| UI-0303 | components/identity/IdentityModule.tsx:2766 | onClick | API_CANDIDATE | targetBatchUserActions |
| UI-0304 | components/identity/IdentityModule.tsx:2774 | button | API_CANDIDATE | targetBatchUserActions |
| UI-0305 | components/identity/IdentityModule.tsx:2775 | onClick | API_CANDIDATE | targetBatchUserActions |
| UI-0306 | components/identity/IdentityModule.tsx:2783 | button | LOCAL_UI |  |
| UI-0307 | components/identity/IdentityModule.tsx:2784 | onClick | LOCAL_UI |  |
| UI-0308 | components/identity/IdentityModule.tsx:2792 | button | LOCAL_UI |  |
| UI-0309 | components/identity/IdentityModule.tsx:2793 | onClick | LOCAL_UI |  |
| UI-0310 | components/identity/IdentityModule.tsx:2801 | button | LOCAL_UI |  |
| UI-0311 | components/identity/IdentityModule.tsx:2802 | onClick | LOCAL_UI |  |
| UI-0312 | components/identity/IdentityModule.tsx:2810 | button | LOCAL_UI |  |
| UI-0313 | components/identity/IdentityModule.tsx:2811 | onClick | LOCAL_UI |  |
| UI-0314 | components/identity/IdentityModule.tsx:2819 | button | API_CANDIDATE | targetBatchUserActions |
| UI-0315 | components/identity/IdentityModule.tsx:2820 | onClick | API_CANDIDATE | targetBatchUserActions |
| UI-0316 | components/identity/IdentityModule.tsx:2828 | button | LOCAL_UI |  |
| UI-0317 | components/identity/IdentityModule.tsx:2829 | onClick | LOCAL_UI |  |
| UI-0318 | components/identity/IdentityModule.tsx:2837 | button | LOCAL_UI |  |
| UI-0319 | components/identity/IdentityModule.tsx:2838 | onClick | LOCAL_UI |  |
| UI-0320 | components/identity/IdentityModule.tsx:2863 | button | LOCAL_UI |  |
| UI-0321 | components/identity/IdentityModule.tsx:2864 | onClick | LOCAL_UI |  |
| UI-0322 | components/identity/IdentityModule.tsx:2895 | button | LOCAL_UI |  |
| UI-0323 | components/identity/IdentityModule.tsx:2897 | onClick | LOCAL_UI |  |
| UI-0324 | components/identity/IdentityModule.tsx:2908 | button | LOCAL_UI |  |
| UI-0325 | components/identity/IdentityModule.tsx:2910 | onClick | LOCAL_UI |  |
| UI-0326 | components/identity/IdentityModule.tsx:2921 | button | LOCAL_UI |  |
| UI-0327 | components/identity/IdentityModule.tsx:2923 | onClick | LOCAL_UI |  |
| UI-0328 | components/identity/IdentityModule.tsx:2941 | select | LOCAL_UI |  |
| UI-0329 | components/identity/IdentityModule.tsx:2943 | onChange | LOCAL_DRAFT |  |
| UI-0330 | components/identity/IdentityModule.tsx:2959 | button | LOCAL_UI |  |
| UI-0331 | components/identity/IdentityModule.tsx:2960 | onClick | LOCAL_UI |  |
| UI-0332 | components/identity/IdentityModule.tsx:2965 | button | API_CANDIDATE | targetBatchUserActions |
| UI-0333 | components/identity/IdentityModule.tsx:2966 | onClick | API_CANDIDATE | targetBatchUserActions |
| UI-0334 | components/identity/IdentityModule.tsx:2991 | button | LOCAL_UI |  |
| UI-0335 | components/identity/IdentityModule.tsx:2992 | onClick | LOCAL_UI |  |
| UI-0336 | components/identity/IdentityModule.tsx:3020 | select | LOCAL_UI |  |
| UI-0337 | components/identity/IdentityModule.tsx:3022 | onChange | LOCAL_DRAFT |  |
| UI-0338 | components/identity/IdentityModule.tsx:3038 | button | LOCAL_UI |  |
| UI-0339 | components/identity/IdentityModule.tsx:3039 | onClick | LOCAL_UI |  |
| UI-0340 | components/identity/IdentityModule.tsx:3044 | button | API_CANDIDATE | targetBatchUserActions |
| UI-0341 | components/identity/IdentityModule.tsx:3045 | onClick | API_CANDIDATE | targetBatchUserActions |
| UI-0342 | components/identity/IdentityModule.tsx:3070 | button | LOCAL_UI |  |
| UI-0343 | components/identity/IdentityModule.tsx:3071 | onClick | LOCAL_UI |  |
| UI-0344 | components/identity/IdentityModule.tsx:3085 | input | LOCAL_UI |  |
| UI-0345 | components/identity/IdentityModule.tsx:3088 | onChange | LOCAL_DRAFT |  |
| UI-0346 | components/identity/IdentityModule.tsx:3102 | input | LOCAL_UI |  |
| UI-0347 | components/identity/IdentityModule.tsx:3105 | onChange | LOCAL_DRAFT |  |
| UI-0348 | components/identity/IdentityModule.tsx:3119 | input | LOCAL_UI |  |
| UI-0349 | components/identity/IdentityModule.tsx:3122 | onChange | LOCAL_DRAFT |  |
| UI-0350 | components/identity/IdentityModule.tsx:3138 | button | LOCAL_UI |  |
| UI-0351 | components/identity/IdentityModule.tsx:3139 | onClick | LOCAL_UI |  |
| UI-0352 | components/identity/IdentityModule.tsx:3144 | button | API_CANDIDATE | targetBatchUserActions |
| UI-0353 | components/identity/IdentityModule.tsx:3145 | onClick | API_CANDIDATE | targetBatchUserActions |
| UI-0354 | components/identity/IdentityModule.tsx:3170 | button | LOCAL_UI |  |
| UI-0355 | components/identity/IdentityModule.tsx:3171 | onClick | LOCAL_UI |  |
| UI-0356 | components/identity/IdentityModule.tsx:3198 | button | LOCAL_UI |  |
| UI-0357 | components/identity/IdentityModule.tsx:3199 | onClick | LOCAL_UI |  |
| UI-0358 | components/identity/IdentityModule.tsx:3204 | button | API_CANDIDATE | targetBatchUserActions |
| UI-0359 | components/identity/IdentityModule.tsx:3205 | onClick | API_CANDIDATE | targetBatchUserActions |
| UI-0360 | components/identity/IdentityModule.tsx:3210 | button | API_CANDIDATE | targetBatchUserActions |
| UI-0361 | components/identity/IdentityModule.tsx:3211 | onClick | API_CANDIDATE | targetBatchUserActions |
| UI-0362 | components/identity/IdentityModule.tsx:3241 | button | LOCAL_UI |  |
| UI-0363 | components/identity/IdentityModule.tsx:3242 | onClick | LOCAL_UI |  |
| UI-0364 | components/identity/IdentityModule.tsx:3253 | button | LOCAL_UI |  |
| UI-0365 | components/identity/IdentityModule.tsx:3255 | onClick | LOCAL_UI |  |
| UI-0366 | components/identity/IdentityModule.tsx:3264 | button | LOCAL_UI |  |
| UI-0367 | components/identity/IdentityModule.tsx:3266 | onClick | LOCAL_UI |  |
| UI-0368 | components/identity/IdentityModule.tsx:3284 | button | LOCAL_UI |  |
| UI-0369 | components/identity/IdentityModule.tsx:3286 | onClick | LOCAL_UI |  |
| UI-0370 | components/identity/IdentityModule.tsx:3292 | button | LOCAL_UI |  |
| UI-0371 | components/identity/IdentityModule.tsx:3294 | onClick | LOCAL_UI |  |
| UI-0372 | components/identity/IdentityModule.tsx:3312 | input | LOCAL_UI |  |
| UI-0373 | components/identity/IdentityModule.tsx:3315 | onChange | LOCAL_DRAFT |  |
| UI-0374 | components/identity/IdentityModule.tsx:3345 | button | LOCAL_UI |  |
| UI-0375 | components/identity/IdentityModule.tsx:3346 | onClick | LOCAL_UI |  |
| UI-0376 | components/identity/IdentityModule.tsx:3351 | button | API_CANDIDATE | targetBatchUserActions |
| UI-0377 | components/identity/IdentityModule.tsx:3352 | onClick | API_CANDIDATE | targetBatchUserActions |
| UI-0378 | components/identity/IdentityModule.tsx:3382 | button | LOCAL_UI |  |
| UI-0379 | components/identity/IdentityModule.tsx:3383 | onClick | LOCAL_UI |  |
| UI-0380 | components/identity/IdentityModule.tsx:3397 | button | LOCAL_UI |  |
| UI-0381 | components/identity/IdentityModule.tsx:3399 | onClick | LOCAL_UI |  |
| UI-0382 | components/identity/IdentityModule.tsx:3405 | button | LOCAL_UI |  |
| UI-0383 | components/identity/IdentityModule.tsx:3407 | onClick | LOCAL_UI |  |
| UI-0384 | components/identity/IdentityModule.tsx:3425 | input | LOCAL_UI |  |
| UI-0385 | components/identity/IdentityModule.tsx:3428 | onChange | LOCAL_DRAFT |  |
| UI-0386 | components/identity/IdentityModule.tsx:3453 | button | LOCAL_UI |  |
| UI-0387 | components/identity/IdentityModule.tsx:3454 | onClick | LOCAL_UI |  |
| UI-0388 | components/identity/IdentityModule.tsx:3459 | button | API_CANDIDATE | targetBatchRoleActions |
| UI-0389 | components/identity/IdentityModule.tsx:3460 | onClick | API_CANDIDATE | targetBatchRoleActions |
| UI-0390 | components/identity/IdentityModule.tsx:3465 | button | API_CANDIDATE | targetBatchRoleActions |
| UI-0391 | components/identity/IdentityModule.tsx:3466 | onClick | API_CANDIDATE | targetBatchRoleActions |
| UI-0392 | components/identity/IdentityModule.tsx:3491 | button | LOCAL_UI |  |
| UI-0393 | components/identity/IdentityModule.tsx:3492 | onClick | LOCAL_UI |  |
| UI-0394 | components/identity/IdentityModule.tsx:3508 | select | LOCAL_UI |  |
| UI-0395 | components/identity/IdentityModule.tsx:3510 | onChange | LOCAL_DRAFT |  |
| UI-0396 | components/identity/IdentityModule.tsx:3523 | button | LOCAL_UI |  |
| UI-0397 | components/identity/IdentityModule.tsx:3524 | onClick | LOCAL_UI |  |
| UI-0398 | components/identity/IdentityModule.tsx:3529 | button | API_CANDIDATE | targetBatchRoleActions |
| UI-0399 | components/identity/IdentityModule.tsx:3530 | onClick | API_CANDIDATE | targetBatchRoleActions |
| UI-0400 | components/identity/IdentityModule.tsx:3555 | button | LOCAL_UI |  |
| UI-0401 | components/identity/IdentityModule.tsx:3556 | onClick | LOCAL_UI |  |
| UI-0402 | components/identity/IdentityModule.tsx:3580 | button | LOCAL_UI |  |
| UI-0403 | components/identity/IdentityModule.tsx:3581 | onClick | LOCAL_UI |  |
| UI-0404 | components/identity/IdentityModule.tsx:3586 | button | API_CANDIDATE | targetBatchRoleActions |
| UI-0405 | components/identity/IdentityModule.tsx:3587 | onClick | API_CANDIDATE | targetBatchRoleActions |
| UI-0406 | components/identity/IdentityModule.tsx:3618 | button | LOCAL_UI |  |
| UI-0407 | components/identity/IdentityModule.tsx:3619 | onClick | LOCAL_UI |  |
| UI-0408 | components/identity/IdentityModule.tsx:3635 | select | LOCAL_UI |  |
| UI-0409 | components/identity/IdentityModule.tsx:3637 | onChange | LOCAL_DRAFT |  |
| UI-0410 | components/identity/IdentityModule.tsx:3650 | button | LOCAL_UI |  |
| UI-0411 | components/identity/IdentityModule.tsx:3651 | onClick | LOCAL_UI |  |
| UI-0412 | components/identity/IdentityModule.tsx:3656 | button | API_CANDIDATE | targetBatchRoleActions |
| UI-0413 | components/identity/IdentityModule.tsx:3657 | onClick | API_CANDIDATE | targetBatchRoleActions |
| UI-0414 | components/identity/IdentityModule.tsx:3689 | button | LOCAL_UI |  |
| UI-0415 | components/identity/IdentityModule.tsx:3690 | onClick | LOCAL_UI |  |
| UI-0416 | components/identity/IdentityModule.tsx:3704 | button | LOCAL_UI |  |
| UI-0417 | components/identity/IdentityModule.tsx:3706 | onClick | LOCAL_UI |  |
| UI-0418 | components/identity/IdentityModule.tsx:3716 | button | LOCAL_UI |  |
| UI-0419 | components/identity/IdentityModule.tsx:3718 | onClick | LOCAL_UI |  |
| UI-0420 | components/identity/IdentityModule.tsx:3728 | button | LOCAL_UI |  |
| UI-0421 | components/identity/IdentityModule.tsx:3730 | onClick | LOCAL_UI |  |
| UI-0422 | components/identity/IdentityModule.tsx:3750 | button | LOCAL_UI |  |
| UI-0423 | components/identity/IdentityModule.tsx:3752 | onClick | LOCAL_UI |  |
| UI-0424 | components/identity/IdentityModule.tsx:3758 | button | LOCAL_UI |  |
| UI-0425 | components/identity/IdentityModule.tsx:3760 | onClick | LOCAL_UI |  |
| UI-0426 | components/identity/IdentityModule.tsx:3778 | input | LOCAL_UI |  |
| UI-0427 | components/identity/IdentityModule.tsx:3781 | onChange | LOCAL_DRAFT |  |
| UI-0428 | components/identity/IdentityModule.tsx:3818 | button | LOCAL_UI |  |
| UI-0429 | components/identity/IdentityModule.tsx:3819 | onClick | LOCAL_UI |  |
| UI-0430 | components/identity/IdentityModule.tsx:3824 | button | API_CANDIDATE | targetBatchUserActions |
| UI-0431 | components/identity/IdentityModule.tsx:3825 | onClick | API_CANDIDATE | targetBatchUserActions |
| UI-0432 | components/identity/IdentityModule.tsx:3856 | button | LOCAL_UI |  |
| UI-0433 | components/identity/IdentityModule.tsx:3857 | onClick | LOCAL_UI |  |
| UI-0434 | components/identity/IdentityModule.tsx:3870 | button | LOCAL_UI |  |
| UI-0435 | components/identity/IdentityModule.tsx:3872 | onClick | LOCAL_UI |  |
| UI-0436 | components/identity/IdentityModule.tsx:3882 | button | LOCAL_UI |  |
| UI-0437 | components/identity/IdentityModule.tsx:3884 | onClick | LOCAL_UI |  |
| UI-0438 | components/identity/IdentityModule.tsx:3903 | button | LOCAL_UI |  |
| UI-0439 | components/identity/IdentityModule.tsx:3905 | onClick | LOCAL_UI |  |
| UI-0440 | components/identity/IdentityModule.tsx:3911 | button | LOCAL_UI |  |
| UI-0441 | components/identity/IdentityModule.tsx:3913 | onClick | LOCAL_UI |  |
| UI-0442 | components/identity/IdentityModule.tsx:3931 | input | LOCAL_UI |  |
| UI-0443 | components/identity/IdentityModule.tsx:3934 | onChange | LOCAL_DRAFT |  |
| UI-0444 | components/identity/IdentityModule.tsx:3971 | button | LOCAL_UI |  |
| UI-0445 | components/identity/IdentityModule.tsx:3972 | onClick | LOCAL_UI |  |
| UI-0446 | components/identity/IdentityModule.tsx:3977 | button | API_CANDIDATE | targetBatchUserActions |
| UI-0447 | components/identity/IdentityModule.tsx:3978 | onClick | API_CANDIDATE | targetBatchUserActions |
| UI-0448 | components/modeler/DmnTableModule.tsx:632 | select | LOCAL_UI |  |
| UI-0449 | components/modeler/DmnTableModule.tsx:634 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0450 | components/modeler/DmnTableModule.tsx:654 | button | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0451 | components/modeler/DmnTableModule.tsx:656 | onClick | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0452 | components/modeler/DmnTableModule.tsx:679 | button | LOCAL_UI |  |
| UI-0453 | components/modeler/DmnTableModule.tsx:680 | onClick | LOCAL_UI |  |
| UI-0454 | components/modeler/DmnTableModule.tsx:690 | button | LOCAL_UI |  |
| UI-0455 | components/modeler/DmnTableModule.tsx:691 | onClick | LOCAL_UI |  |
| UI-0456 | components/modeler/DmnTableModule.tsx:706 | button | LOCAL_UI |  |
| UI-0457 | components/modeler/DmnTableModule.tsx:707 | onClick | LOCAL_UI |  |
| UI-0458 | components/modeler/DmnTableModule.tsx:737 | button | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0459 | components/modeler/DmnTableModule.tsx:738 | onClick | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0460 | components/modeler/DmnTableModule.tsx:747 | button | LOCAL_UI |  |
| UI-0461 | components/modeler/DmnTableModule.tsx:748 | onClick | LOCAL_UI |  |
| UI-0462 | components/modeler/DmnTableModule.tsx:775 | button | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0463 | components/modeler/DmnTableModule.tsx:776 | onClick | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0464 | components/modeler/DmnTableModule.tsx:785 | button | API_CANDIDATE | targetExportDecisionModel |
| UI-0465 | components/modeler/DmnTableModule.tsx:786 | onClick | API_CANDIDATE | targetExportDecisionModel |
| UI-0466 | components/modeler/DmnTableModule.tsx:795 | button | LOCAL_UI |  |
| UI-0467 | components/modeler/DmnTableModule.tsx:796 | onClick | LOCAL_UI |  |
| UI-0468 | components/modeler/DmnTableModule.tsx:805 | button | LOCAL_UI |  |
| UI-0469 | components/modeler/DmnTableModule.tsx:806 | onClick | LOCAL_UI |  |
| UI-0470 | components/modeler/DmnTableModule.tsx:830 | onUpdateDecision | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0471 | components/modeler/DmnTableModule.tsx:833 | onRunTest | API_CANDIDATE | targetEvaluateDecision |
| UI-0472 | components/modeler/DmnTableModule.tsx:834 | onExportDmn | API_CANDIDATE | targetExportDecisionModel |
| UI-0473 | components/modeler/DmnTableModule.tsx:862 | input | LOCAL_UI |  |
| UI-0474 | components/modeler/DmnTableModule.tsx:866 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0475 | components/modeler/DmnTableModule.tsx:902 | button | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0476 | components/modeler/DmnTableModule.tsx:903 | onClick | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0477 | components/modeler/DmnTableModule.tsx:909 | button | LOCAL_UI |  |
| UI-0478 | components/modeler/DmnTableModule.tsx:910 | onClick | LOCAL_UI |  |
| UI-0479 | components/modeler/DmnTableModule.tsx:937 | onClick | LOCAL_UI |  |
| UI-0480 | components/modeler/DmnTableModule.tsx:968 | button | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0481 | components/modeler/DmnTableModule.tsx:969 | onClick | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0482 | components/modeler/DmnTableModule.tsx:1076 | button | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0483 | components/modeler/DmnTableModule.tsx:1077 | onClick | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0484 | components/modeler/DmnTableModule.tsx:1094 | button | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0485 | components/modeler/DmnTableModule.tsx:1095 | onClick | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0486 | components/modeler/DmnTableModule.tsx:1206 | button | LOCAL_UI |  |
| UI-0487 | components/modeler/DmnTableModule.tsx:1208 | onClick | LOCAL_UI |  |
| UI-0488 | components/modeler/DmnTableModule.tsx:1218 | button | LOCAL_UI |  |
| UI-0489 | components/modeler/DmnTableModule.tsx:1220 | onClick | LOCAL_UI |  |
| UI-0490 | components/modeler/DmnTableModule.tsx:1252 | input | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0491 | components/modeler/DmnTableModule.tsx:1255 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0492 | components/modeler/DmnTableModule.tsx:1291 | input | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0493 | components/modeler/DmnTableModule.tsx:1294 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0494 | components/modeler/DmnTableModule.tsx:1315 | input | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0495 | components/modeler/DmnTableModule.tsx:1318 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0496 | components/modeler/DmnTableModule.tsx:1330 | button | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0497 | components/modeler/DmnTableModule.tsx:1331 | onClick | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0498 | components/modeler/DmnTableModule.tsx:1337 | button | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0499 | components/modeler/DmnTableModule.tsx:1338 | onClick | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0500 | components/modeler/DmnTableModule.tsx:1398 | button | LOCAL_UI |  |
| UI-0501 | components/modeler/DmnTableModule.tsx:1401 | onClick | LOCAL_UI |  |
| UI-0502 | components/modeler/DmnTableModule.tsx:1434 | select | LOCAL_UI |  |
| UI-0503 | components/modeler/DmnTableModule.tsx:1436 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0504 | components/modeler/DmnTableModule.tsx:1445 | button | LOCAL_UI |  |
| UI-0505 | components/modeler/DmnTableModule.tsx:1447 | onClick | LOCAL_UI |  |
| UI-0506 | components/modeler/DmnTableModule.tsx:1454 | button | LOCAL_UI |  |
| UI-0507 | components/modeler/DmnTableModule.tsx:1456 | onClick | LOCAL_UI |  |
| UI-0508 | components/modeler/DmnTableModule.tsx:1465 | input | LOCAL_UI |  |
| UI-0509 | components/modeler/DmnTableModule.tsx:1468 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0510 | components/modeler/DmnTableModule.tsx:1481 | button | API_CANDIDATE | targetEvaluateDecision |
| UI-0511 | components/modeler/DmnTableModule.tsx:1483 | onClick | API_CANDIDATE | targetEvaluateDecision |
| UI-0512 | components/modeler/DmnTableModule.tsx:1555 | onClose | LOCAL_UI |  |
| UI-0513 | components/modeler/DmnTableModule.tsx:1558 | onSaveSnapshot | API_CANDIDATE | targetCheckinDecisionModel |
| UI-0514 | components/modeler/DmnTableModule.tsx:1559 | onRevertToVersion | API_CANDIDATE | targetRevertDecisionModel |
| UI-0515 | components/modeler/DmnTableModule.tsx:1560 | onCompareVersions | LOCAL_UI |  |
| UI-0516 | components/modeler/DmnTableModule.tsx:1561 | onInspectVersion | LOCAL_UI |  |
| UI-0517 | components/modeler/DmnTableModule.tsx:1582 | button | LOCAL_UI |  |
| UI-0518 | components/modeler/DmnTableModule.tsx:1583 | onClick | LOCAL_UI |  |
| UI-0519 | components/modeler/DmnTableModule.tsx:1590 | textarea | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0520 | components/modeler/DmnTableModule.tsx:1592 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0521 | components/modeler/DmnTableModule.tsx:1600 | button | LOCAL_UI |  |
| UI-0522 | components/modeler/DmnTableModule.tsx:1601 | onClick | LOCAL_UI |  |
| UI-0523 | components/modeler/DmnTableModule.tsx:1608 | button | LOCAL_UI |  |
| UI-0524 | components/modeler/DmnTableModule.tsx:1609 | onClick | LOCAL_UI |  |
| UI-0525 | components/modeler/DmnTableModule.tsx:1625 | button | LOCAL_UI |  |
| UI-0526 | components/modeler/DmnTableModule.tsx:1626 | onClick | LOCAL_UI |  |
| UI-0527 | components/modeler/DmnTableModule.tsx:1644 | onClose | LOCAL_UI |  |
| UI-0528 | components/modeler/DmnTableModule.tsx:1648 | onRevertToVersion | API_CANDIDATE | targetRevertDecisionModel |
| UI-0529 | components/modeler/DmnTableModule.tsx:1656 | onClose | LOCAL_UI |  |
| UI-0530 | components/modeler/DmnTableModule.tsx:1657 | onRevert | API_CANDIDATE | targetRevertDecisionModel |
| UI-0531 | components/modeler/DmnVersionCompareModal.tsx:94 | button | LOCAL_UI |  |
| UI-0532 | components/modeler/DmnVersionCompareModal.tsx:95 | onClick | LOCAL_UI |  |
| UI-0533 | components/modeler/DmnVersionCompareModal.tsx:108 | select | LOCAL_UI |  |
| UI-0534 | components/modeler/DmnVersionCompareModal.tsx:110 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0535 | components/modeler/DmnVersionCompareModal.tsx:125 | select | LOCAL_UI |  |
| UI-0536 | components/modeler/DmnVersionCompareModal.tsx:127 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0537 | components/modeler/DmnVersionCompareModal.tsx:348 | button | LOCAL_UI |  |
| UI-0538 | components/modeler/DmnVersionCompareModal.tsx:349 | onClick | LOCAL_UI |  |
| UI-0539 | components/modeler/DmnVersionCompareModal.tsx:356 | button | API_CANDIDATE | targetRevertDecisionModel |
| UI-0540 | components/modeler/DmnVersionCompareModal.tsx:357 | onClick | API_CANDIDATE | targetRevertDecisionModel |
| UI-0541 | components/modeler/DmnVersionHistorySidebar.tsx:108 | button | LOCAL_UI |  |
| UI-0542 | components/modeler/DmnVersionHistorySidebar.tsx:109 | onClick | LOCAL_UI |  |
| UI-0543 | components/modeler/DmnVersionHistorySidebar.tsx:120 | button | LOCAL_UI |  |
| UI-0544 | components/modeler/DmnVersionHistorySidebar.tsx:121 | onClick | LOCAL_UI |  |
| UI-0545 | components/modeler/DmnVersionHistorySidebar.tsx:128 | form | API_CANDIDATE | targetCheckinDecisionModel |
| UI-0546 | components/modeler/DmnVersionHistorySidebar.tsx:128 | onSubmit | API_CANDIDATE | targetCheckinDecisionModel |
| UI-0547 | components/modeler/DmnVersionHistorySidebar.tsx:133 | button | LOCAL_UI |  |
| UI-0548 | components/modeler/DmnVersionHistorySidebar.tsx:135 | onClick | LOCAL_UI |  |
| UI-0549 | components/modeler/DmnVersionHistorySidebar.tsx:145 | input | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0550 | components/modeler/DmnVersionHistorySidebar.tsx:148 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0551 | components/modeler/DmnVersionHistorySidebar.tsx:156 | input | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0552 | components/modeler/DmnVersionHistorySidebar.tsx:159 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0553 | components/modeler/DmnVersionHistorySidebar.tsx:168 | textarea | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0554 | components/modeler/DmnVersionHistorySidebar.tsx:170 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0555 | components/modeler/DmnVersionHistorySidebar.tsx:177 | button | NO_DIRECT_HANDLER |  |
| UI-0556 | components/modeler/DmnVersionHistorySidebar.tsx:188 | input | LOCAL_UI |  |
| UI-0557 | components/modeler/DmnVersionHistorySidebar.tsx:191 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0558 | components/modeler/DmnVersionHistorySidebar.tsx:277 | button | LOCAL_UI |  |
| UI-0559 | components/modeler/DmnVersionHistorySidebar.tsx:278 | onClick | LOCAL_UI |  |
| UI-0560 | components/modeler/DmnVersionHistorySidebar.tsx:287 | button | LOCAL_UI |  |
| UI-0561 | components/modeler/DmnVersionHistorySidebar.tsx:288 | onClick | LOCAL_UI |  |
| UI-0562 | components/modeler/DmnVersionHistorySidebar.tsx:298 | button | LOCAL_UI |  |
| UI-0563 | components/modeler/DmnVersionHistorySidebar.tsx:299 | onClick | LOCAL_UI |  |
| UI-0564 | components/modeler/DmnVersionHistorySidebar.tsx:327 | button | LOCAL_UI |  |
| UI-0565 | components/modeler/DmnVersionHistorySidebar.tsx:328 | onClick | LOCAL_UI |  |
| UI-0566 | components/modeler/DmnVersionHistorySidebar.tsx:333 | button | API_CANDIDATE | targetRevertDecisionModel |
| UI-0567 | components/modeler/DmnVersionHistorySidebar.tsx:334 | onClick | API_CANDIDATE | targetRevertDecisionModel |
| UI-0568 | components/modeler/DmnVersionInspectModal.tsx:54 | button | LOCAL_UI |  |
| UI-0569 | components/modeler/DmnVersionInspectModal.tsx:55 | onClick | LOCAL_UI |  |
| UI-0570 | components/modeler/DmnVersionInspectModal.tsx:136 | button | LOCAL_UI |  |
| UI-0571 | components/modeler/DmnVersionInspectModal.tsx:137 | onClick | LOCAL_UI |  |
| UI-0572 | components/modeler/DmnVersionInspectModal.tsx:143 | button | API_CANDIDATE | targetRevertDecisionModel |
| UI-0573 | components/modeler/DmnVersionInspectModal.tsx:144 | onClick | API_CANDIDATE | targetRevertDecisionModel |
| UI-0574 | components/modeler/DmnVisualLogicEditor.tsx:240 | input | LOCAL_UI |  |
| UI-0575 | components/modeler/DmnVisualLogicEditor.tsx:244 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0576 | components/modeler/DmnVisualLogicEditor.tsx:251 | button | API_CANDIDATE | targetEvaluateDecision |
| UI-0577 | components/modeler/DmnVisualLogicEditor.tsx:252 | onClick | API_CANDIDATE | targetEvaluateDecision |
| UI-0578 | components/modeler/DmnVisualLogicEditor.tsx:262 | button | API_CANDIDATE | targetExportDecisionModel |
| UI-0579 | components/modeler/DmnVisualLogicEditor.tsx:263 | onClick | API_CANDIDATE | targetExportDecisionModel |
| UI-0580 | components/modeler/DmnVisualLogicEditor.tsx:273 | button | LOCAL_UI |  |
| UI-0581 | components/modeler/DmnVisualLogicEditor.tsx:274 | onClick | LOCAL_UI |  |
| UI-0582 | components/modeler/DmnVisualLogicEditor.tsx:305 | onClick | LOCAL_UI |  |
| UI-0583 | components/modeler/DmnVisualLogicEditor.tsx:363 | button | LOCAL_UI |  |
| UI-0584 | components/modeler/DmnVisualLogicEditor.tsx:364 | onClick | LOCAL_UI |  |
| UI-0585 | components/modeler/DmnVisualLogicEditor.tsx:435 | button | LOCAL_UI |  |
| UI-0586 | components/modeler/DmnVisualLogicEditor.tsx:436 | onClick | LOCAL_UI |  |
| UI-0587 | components/modeler/DmnVisualLogicEditor.tsx:443 | button | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0588 | components/modeler/DmnVisualLogicEditor.tsx:444 | onClick | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0589 | components/modeler/DmnVisualLogicEditor.tsx:450 | button | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0590 | components/modeler/DmnVisualLogicEditor.tsx:451 | onClick | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0591 | components/modeler/DmnVisualLogicEditor.tsx:504 | onClick | LOCAL_UI |  |
| UI-0592 | components/modeler/DmnVisualLogicEditor.tsx:542 | onClick | LOCAL_UI |  |
| UI-0593 | components/modeler/DmnVisualLogicEditor.tsx:592 | onClick | LOCAL_UI |  |
| UI-0594 | components/modeler/DmnVisualLogicEditor.tsx:646 | button | LOCAL_UI |  |
| UI-0595 | components/modeler/DmnVisualLogicEditor.tsx:647 | onClick | LOCAL_UI |  |
| UI-0596 | components/modeler/DmnVisualLogicEditor.tsx:661 | input | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0597 | components/modeler/DmnVisualLogicEditor.tsx:664 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0598 | components/modeler/DmnVisualLogicEditor.tsx:689 | select | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0599 | components/modeler/DmnVisualLogicEditor.tsx:691 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0600 | components/modeler/DmnVisualLogicEditor.tsx:711 | select | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0601 | components/modeler/DmnVisualLogicEditor.tsx:713 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0602 | components/modeler/DmnVisualLogicEditor.tsx:726 | input | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0603 | components/modeler/DmnVisualLogicEditor.tsx:729 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0604 | components/modeler/DmnVisualLogicEditor.tsx:765 | select | LOCAL_UI |  |
| UI-0605 | components/modeler/DmnVisualLogicEditor.tsx:767 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0606 | components/modeler/DmnVisualLogicEditor.tsx:776 | button | LOCAL_UI |  |
| UI-0607 | components/modeler/DmnVisualLogicEditor.tsx:778 | onClick | LOCAL_UI |  |
| UI-0608 | components/modeler/DmnVisualLogicEditor.tsx:785 | button | LOCAL_UI |  |
| UI-0609 | components/modeler/DmnVisualLogicEditor.tsx:787 | onClick | LOCAL_UI |  |
| UI-0610 | components/modeler/DmnVisualLogicEditor.tsx:796 | input | LOCAL_UI |  |
| UI-0611 | components/modeler/DmnVisualLogicEditor.tsx:799 | onChange | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0612 | components/modeler/DmnVisualLogicEditor.tsx:819 | button | LOCAL_UI |  |
| UI-0613 | components/modeler/DmnVisualLogicEditor.tsx:820 | onClick | LOCAL_UI |  |
| UI-0614 | components/modeler/DmnVisualLogicEditor.tsx:825 | button | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0615 | components/modeler/DmnVisualLogicEditor.tsx:826 | onClick | LOCAL_DRAFT | targetSaveDecisionModelDraft |
| UI-0616 | components/modeler/ModelerModule.tsx:380 | button | LOCAL_UI |  |
| UI-0617 | components/modeler/ModelerModule.tsx:381 | onClick | LOCAL_UI |  |
| UI-0618 | components/modeler/ModelerModule.tsx:392 | button | LOCAL_UI |  |
| UI-0619 | components/modeler/ModelerModule.tsx:393 | onClick | LOCAL_UI |  |
| UI-0620 | components/modeler/ModelerModule.tsx:410 | select | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0621 | components/modeler/ModelerModule.tsx:412 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0622 | components/modeler/ModelerModule.tsx:435 | button | LOCAL_UI |  |
| UI-0623 | components/modeler/ModelerModule.tsx:436 | onClick | LOCAL_UI |  |
| UI-0624 | components/modeler/ModelerModule.tsx:445 | button | LOCAL_UI |  |
| UI-0625 | components/modeler/ModelerModule.tsx:446 | onClick | LOCAL_UI |  |
| UI-0626 | components/modeler/ModelerModule.tsx:452 | button | LOCAL_UI |  |
| UI-0627 | components/modeler/ModelerModule.tsx:453 | onClick | LOCAL_UI |  |
| UI-0628 | components/modeler/ModelerModule.tsx:462 | button | LOCAL_UI |  |
| UI-0629 | components/modeler/ModelerModule.tsx:463 | onClick | LOCAL_UI |  |
| UI-0630 | components/modeler/ModelerModule.tsx:471 | button | LOCAL_UI |  |
| UI-0631 | components/modeler/ModelerModule.tsx:472 | onClick | LOCAL_UI |  |
| UI-0632 | components/modeler/ModelerModule.tsx:487 | onDeployDecision | API_CANDIDATE | targetCreateDeployment |
| UI-0633 | components/modeler/ModelerModule.tsx:496 | onAddNode | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0634 | components/modeler/ModelerModule.tsx:498 | onToggleExpand | LOCAL_UI |  |
| UI-0635 | components/modeler/ModelerModule.tsx:504 | onMouseMove | LOCAL_UI |  |
| UI-0636 | components/modeler/ModelerModule.tsx:505 | onMouseUp | LOCAL_UI |  |
| UI-0637 | components/modeler/ModelerModule.tsx:506 | onMouseDown | LOCAL_UI |  |
| UI-0638 | components/modeler/ModelerModule.tsx:519 | button | LOCAL_UI |  |
| UI-0639 | components/modeler/ModelerModule.tsx:520 | onClick | LOCAL_UI |  |
| UI-0640 | components/modeler/ModelerModule.tsx:529 | button | LOCAL_UI |  |
| UI-0641 | components/modeler/ModelerModule.tsx:530 | onClick | LOCAL_UI |  |
| UI-0642 | components/modeler/ModelerModule.tsx:539 | button | LOCAL_UI |  |
| UI-0643 | components/modeler/ModelerModule.tsx:540 | onClick | LOCAL_UI |  |
| UI-0644 | components/modeler/ModelerModule.tsx:552 | button | LOCAL_UI |  |
| UI-0645 | components/modeler/ModelerModule.tsx:553 | onClick | LOCAL_UI |  |
| UI-0646 | components/modeler/ModelerModule.tsx:570 | button | LOCAL_UI |  |
| UI-0647 | components/modeler/ModelerModule.tsx:571 | onClick | LOCAL_UI |  |
| UI-0648 | components/modeler/ModelerModule.tsx:577 | button | LOCAL_UI |  |
| UI-0649 | components/modeler/ModelerModule.tsx:578 | onClick | LOCAL_UI |  |
| UI-0650 | components/modeler/ModelerModule.tsx:590 | button | LOCAL_UI |  |
| UI-0651 | components/modeler/ModelerModule.tsx:591 | onClick | LOCAL_UI |  |
| UI-0652 | components/modeler/ModelerModule.tsx:810 | onMouseDown | LOCAL_UI |  |
| UI-0653 | components/modeler/ModelerModule.tsx:1001 | onClick | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0654 | components/modeler/ModelerModule.tsx:1012 | onClick | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0655 | components/modeler/ModelerModule.tsx:1023 | onClick | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0656 | components/modeler/ModelerModule.tsx:1034 | onClick | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0657 | components/modeler/ModelerModule.tsx:1055 | onUpdateNode | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0658 | components/modeler/ModelerModule.tsx:1059 | onUpdateProcess | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0659 | components/modeler/ModelerModule.tsx:1061 | onOpenDmn | LOCAL_UI |  |
| UI-0660 | components/modeler/ModelerModule.tsx:1086 | button | LOCAL_UI |  |
| UI-0661 | components/modeler/ModelerModule.tsx:1087 | onClick | LOCAL_UI |  |
| UI-0662 | components/modeler/ModelerModule.tsx:1094 | textarea | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0663 | components/modeler/ModelerModule.tsx:1096 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0664 | components/modeler/ModelerModule.tsx:1104 | button | LOCAL_UI |  |
| UI-0665 | components/modeler/ModelerModule.tsx:1105 | onClick | LOCAL_UI |  |
| UI-0666 | components/modeler/ModelerModule.tsx:1117 | button | LOCAL_UI |  |
| UI-0667 | components/modeler/ModelerModule.tsx:1118 | onClick | LOCAL_UI |  |
| UI-0668 | components/modeler/ModelerModule.tsx:1142 | button | LOCAL_UI |  |
| UI-0669 | components/modeler/ModelerModule.tsx:1142 | onClick | LOCAL_UI |  |
| UI-0670 | components/modeler/ModelerModule.tsx:1151 | button | LOCAL_UI |  |
| UI-0671 | components/modeler/ModelerModule.tsx:1153 | onClick | LOCAL_UI |  |
| UI-0672 | components/modeler/ModelerModule.tsx:1189 | button | LOCAL_UI |  |
| UI-0673 | components/modeler/ModelerModule.tsx:1190 | onClick | LOCAL_UI |  |
| UI-0674 | components/modeler/ModelerModule.tsx:1195 | button | LOCAL_UI |  |
| UI-0675 | components/modeler/ModelerModule.tsx:1196 | onClick | LOCAL_UI |  |
| UI-0676 | components/modeler/ModelerPalette.tsx:298 | button | LOCAL_UI |  |
| UI-0677 | components/modeler/ModelerPalette.tsx:299 | onClick | LOCAL_UI |  |
| UI-0678 | components/modeler/ModelerPalette.tsx:312 | input | LOCAL_UI |  |
| UI-0679 | components/modeler/ModelerPalette.tsx:316 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0680 | components/modeler/ModelerPalette.tsx:330 | button | LOCAL_UI |  |
| UI-0681 | components/modeler/ModelerPalette.tsx:331 | onClick | LOCAL_UI |  |
| UI-0682 | components/modeler/ModelerPalette.tsx:344 | onDragEnd | LOCAL_UI |  |
| UI-0683 | components/modeler/ModelerPalette.tsx:345 | onClick | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0684 | components/modeler/ModelerPalette.tsx:372 | button | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0685 | components/modeler/ModelerPalette.tsx:374 | onClick | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0686 | components/modeler/ModelerPalette.tsx:376 | onDragEnd | LOCAL_UI |  |
| UI-0687 | components/modeler/ModelerPropertiesPanel.tsx:123 | button | LOCAL_UI |  |
| UI-0688 | components/modeler/ModelerPropertiesPanel.tsx:124 | onClick | LOCAL_UI |  |
| UI-0689 | components/modeler/ModelerPropertiesPanel.tsx:132 | button | LOCAL_UI |  |
| UI-0690 | components/modeler/ModelerPropertiesPanel.tsx:133 | onClick | LOCAL_UI |  |
| UI-0691 | components/modeler/ModelerPropertiesPanel.tsx:141 | button | LOCAL_UI |  |
| UI-0692 | components/modeler/ModelerPropertiesPanel.tsx:142 | onClick | LOCAL_UI |  |
| UI-0693 | components/modeler/ModelerPropertiesPanel.tsx:150 | button | LOCAL_UI |  |
| UI-0694 | components/modeler/ModelerPropertiesPanel.tsx:151 | onClick | LOCAL_UI |  |
| UI-0695 | components/modeler/ModelerPropertiesPanel.tsx:159 | button | LOCAL_UI |  |
| UI-0696 | components/modeler/ModelerPropertiesPanel.tsx:160 | onClick | LOCAL_UI |  |
| UI-0697 | components/modeler/ModelerPropertiesPanel.tsx:180 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0698 | components/modeler/ModelerPropertiesPanel.tsx:183 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0699 | components/modeler/ModelerPropertiesPanel.tsx:195 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0700 | components/modeler/ModelerPropertiesPanel.tsx:198 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0701 | components/modeler/ModelerPropertiesPanel.tsx:210 | select | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0702 | components/modeler/ModelerPropertiesPanel.tsx:212 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0703 | components/modeler/ModelerPropertiesPanel.tsx:224 | textarea | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0704 | components/modeler/ModelerPropertiesPanel.tsx:227 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0705 | components/modeler/ModelerPropertiesPanel.tsx:238 | input | NO_DIRECT_HANDLER |  |
| UI-0706 | components/modeler/ModelerPropertiesPanel.tsx:253 | select | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0707 | components/modeler/ModelerPropertiesPanel.tsx:255 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0708 | components/modeler/ModelerPropertiesPanel.tsx:291 | button | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0709 | components/modeler/ModelerPropertiesPanel.tsx:292 | onClick | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0710 | components/modeler/ModelerPropertiesPanel.tsx:301 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0711 | components/modeler/ModelerPropertiesPanel.tsx:306 | onKeyDown | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0712 | components/modeler/ModelerPropertiesPanel.tsx:316 | button | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0713 | components/modeler/ModelerPropertiesPanel.tsx:317 | onClick | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0714 | components/modeler/ModelerPropertiesPanel.tsx:336 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0715 | components/modeler/ModelerPropertiesPanel.tsx:339 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0716 | components/modeler/ModelerPropertiesPanel.tsx:350 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0717 | components/modeler/ModelerPropertiesPanel.tsx:353 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0718 | components/modeler/ModelerPropertiesPanel.tsx:364 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0719 | components/modeler/ModelerPropertiesPanel.tsx:367 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0720 | components/modeler/ModelerPropertiesPanel.tsx:377 | select | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0721 | components/modeler/ModelerPropertiesPanel.tsx:379 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0722 | components/modeler/ModelerPropertiesPanel.tsx:407 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0723 | components/modeler/ModelerPropertiesPanel.tsx:410 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0724 | components/modeler/ModelerPropertiesPanel.tsx:419 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0725 | components/modeler/ModelerPropertiesPanel.tsx:422 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0726 | components/modeler/ModelerPropertiesPanel.tsx:440 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0727 | components/modeler/ModelerPropertiesPanel.tsx:443 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0728 | components/modeler/ModelerPropertiesPanel.tsx:451 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0729 | components/modeler/ModelerPropertiesPanel.tsx:454 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0730 | components/modeler/ModelerPropertiesPanel.tsx:459 | button | LOCAL_UI |  |
| UI-0731 | components/modeler/ModelerPropertiesPanel.tsx:461 | onClick | LOCAL_UI |  |
| UI-0732 | components/modeler/ModelerPropertiesPanel.tsx:482 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0733 | components/modeler/ModelerPropertiesPanel.tsx:485 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0734 | components/modeler/ModelerPropertiesPanel.tsx:491 | input | NO_DIRECT_HANDLER |  |
| UI-0735 | components/modeler/ModelerPropertiesPanel.tsx:503 | select | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0736 | components/modeler/ModelerPropertiesPanel.tsx:505 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0737 | components/modeler/ModelerPropertiesPanel.tsx:517 | textarea | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0738 | components/modeler/ModelerPropertiesPanel.tsx:520 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0739 | components/modeler/ModelerPropertiesPanel.tsx:533 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0740 | components/modeler/ModelerPropertiesPanel.tsx:536 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0741 | components/modeler/ModelerPropertiesPanel.tsx:546 | input | NO_DIRECT_HANDLER |  |
| UI-0742 | components/modeler/ModelerPropertiesPanel.tsx:550 | input | NO_DIRECT_HANDLER |  |
| UI-0743 | components/modeler/ModelerPropertiesPanel.tsx:568 | button | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0744 | components/modeler/ModelerPropertiesPanel.tsx:569 | onClick | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0745 | components/modeler/ModelerPropertiesPanel.tsx:582 | input | NO_DIRECT_HANDLER |  |
| UI-0746 | components/modeler/ModelerPropertiesPanel.tsx:589 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0747 | components/modeler/ModelerPropertiesPanel.tsx:595 | button | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0748 | components/modeler/ModelerPropertiesPanel.tsx:596 | onClick | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0749 | components/modeler/ModelerPropertiesPanel.tsx:612 | button | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0750 | components/modeler/ModelerPropertiesPanel.tsx:613 | onClick | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0751 | components/modeler/ModelerPropertiesPanel.tsx:625 | input | NO_DIRECT_HANDLER |  |
| UI-0752 | components/modeler/ModelerPropertiesPanel.tsx:632 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0753 | components/modeler/ModelerPropertiesPanel.tsx:638 | button | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0754 | components/modeler/ModelerPropertiesPanel.tsx:639 | onClick | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0755 | components/modeler/ModelerPropertiesPanel.tsx:662 | button | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0756 | components/modeler/ModelerPropertiesPanel.tsx:663 | onClick | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0757 | components/modeler/ModelerPropertiesPanel.tsx:670 | button | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0758 | components/modeler/ModelerPropertiesPanel.tsx:671 | onClick | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0759 | components/modeler/ModelerPropertiesPanel.tsx:678 | button | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0760 | components/modeler/ModelerPropertiesPanel.tsx:679 | onClick | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0761 | components/modeler/ModelerPropertiesPanel.tsx:692 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0762 | components/modeler/ModelerPropertiesPanel.tsx:695 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0763 | components/modeler/ModelerPropertiesPanel.tsx:701 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0764 | components/modeler/ModelerPropertiesPanel.tsx:704 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0765 | components/modeler/ModelerPropertiesPanel.tsx:710 | input | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0766 | components/modeler/ModelerPropertiesPanel.tsx:714 | onChange | LOCAL_DRAFT | targetSaveProcessModelDraft |
| UI-0767 | components/modeler/ModelerPropertiesPanel.tsx:733 | input | NO_DIRECT_HANDLER |  |
| UI-0768 | components/operation/OperationModule.tsx:248 | button | LOCAL_UI |  |
| UI-0769 | components/operation/OperationModule.tsx:249 | onClick | LOCAL_UI |  |
| UI-0770 | components/operation/OperationModule.tsx:256 | button | LOCAL_UI |  |
| UI-0771 | components/operation/OperationModule.tsx:257 | onClick | LOCAL_UI |  |
| UI-0772 | components/operation/OperationModule.tsx:264 | button | API_CANDIDATE | targetBatchInstanceActions;targetCancelInstance |
| UI-0773 | components/operation/OperationModule.tsx:265 | onClick | API_CANDIDATE | targetBatchInstanceActions;targetCancelInstance |
| UI-0774 | components/operation/OperationModule.tsx:281 | select | LOCAL_UI |  |
| UI-0775 | components/operation/OperationModule.tsx:283 | onChange | LOCAL_DRAFT |  |
| UI-0776 | components/operation/OperationModule.tsx:295 | select | LOCAL_UI |  |
| UI-0777 | components/operation/OperationModule.tsx:297 | onChange | LOCAL_DRAFT |  |
| UI-0778 | components/operation/OperationModule.tsx:309 | input | LOCAL_UI |  |
| UI-0779 | components/operation/OperationModule.tsx:313 | onChange | LOCAL_DRAFT |  |
| UI-0780 | components/operation/OperationModule.tsx:320 | input | LOCAL_UI |  |
| UI-0781 | components/operation/OperationModule.tsx:324 | onChange | LOCAL_DRAFT |  |
| UI-0782 | components/operation/OperationModule.tsx:333 | input | LOCAL_UI |  |
| UI-0783 | components/operation/OperationModule.tsx:336 | onChange | LOCAL_DRAFT |  |
| UI-0784 | components/operation/OperationModule.tsx:342 | input | LOCAL_UI |  |
| UI-0785 | components/operation/OperationModule.tsx:345 | onChange | LOCAL_DRAFT |  |
| UI-0786 | components/operation/OperationModule.tsx:356 | input | LOCAL_UI |  |
| UI-0787 | components/operation/OperationModule.tsx:359 | onChange | LOCAL_DRAFT |  |
| UI-0788 | components/operation/OperationModule.tsx:365 | input | LOCAL_UI |  |
| UI-0789 | components/operation/OperationModule.tsx:368 | onChange | LOCAL_DRAFT |  |
| UI-0790 | components/operation/OperationModule.tsx:377 | button | LOCAL_UI |  |
| UI-0791 | components/operation/OperationModule.tsx:378 | onClick | LOCAL_UI |  |
| UI-0792 | components/operation/OperationModule.tsx:392 | button | API_CANDIDATE | queryProcesses |
| UI-0793 | components/operation/OperationModule.tsx:393 | onClick | API_CANDIDATE | queryProcesses |
| UI-0794 | components/operation/OperationModule.tsx:429 | button | LOCAL_UI |  |
| UI-0795 | components/operation/OperationModule.tsx:430 | onClick | LOCAL_UI |  |
| UI-0796 | components/operation/OperationModule.tsx:443 | button | LOCAL_UI |  |
| UI-0797 | components/operation/OperationModule.tsx:444 | onClick | LOCAL_UI |  |
| UI-0798 | components/operation/OperationModule.tsx:457 | button | LOCAL_UI |  |
| UI-0799 | components/operation/OperationModule.tsx:458 | onClick | LOCAL_UI |  |
| UI-0800 | components/operation/OperationModule.tsx:473 | button | LOCAL_UI |  |
| UI-0801 | components/operation/OperationModule.tsx:474 | onClick | LOCAL_UI |  |
| UI-0802 | components/operation/OperationModule.tsx:590 | onClick | LOCAL_UI |  |
| UI-0803 | components/operation/OperationModule.tsx:628 | onClick | LOCAL_UI |  |
| UI-0804 | components/operation/OperationModule.tsx:698 | button | LOCAL_UI |  |
| UI-0805 | components/operation/OperationModule.tsx:699 | onClick | LOCAL_UI |  |
| UI-0806 | components/operation/OperationModule.tsx:723 | input | LOCAL_UI |  |
| UI-0807 | components/operation/OperationModule.tsx:726 | onChange | LOCAL_UI |  |
| UI-0808 | components/operation/OperationModule.tsx:748 | onClick | LOCAL_UI |  |
| UI-0809 | components/operation/OperationModule.tsx:753 | onClick | LOCAL_UI |  |
| UI-0810 | components/operation/OperationModule.tsx:754 | input | LOCAL_UI |  |
| UI-0811 | components/operation/OperationModule.tsx:757 | onChange | LOCAL_UI |  |
| UI-0812 | components/operation/OperationModule.tsx:796 | onClick | LOCAL_UI |  |
| UI-0813 | components/operation/OperationModule.tsx:797 | button | API_CANDIDATE | targetBatchInstanceActions |
| UI-0814 | components/operation/OperationModule.tsx:798 | onClick | API_CANDIDATE | targetBatchInstanceActions |
| UI-0815 | components/operation/OperationModule.tsx:831 | button | LOCAL_UI |  |
| UI-0816 | components/operation/OperationModule.tsx:831 | onClick | LOCAL_UI |  |
| UI-0817 | components/operation/OperationModule.tsx:839 | select | LOCAL_DRAFT |  |
| UI-0818 | components/operation/OperationModule.tsx:841 | onChange | LOCAL_DRAFT |  |
| UI-0819 | components/operation/OperationModule.tsx:853 | button | LOCAL_UI |  |
| UI-0820 | components/operation/OperationModule.tsx:853 | onClick | LOCAL_UI |  |
| UI-0821 | components/operation/OperationModule.tsx:856 | button | API_CANDIDATE | targetDryRunMigration;targetExecuteMigration |
| UI-0822 | components/operation/OperationModule.tsx:856 | onClick | API_CANDIDATE | targetDryRunMigration;targetExecuteMigration |
| UI-0823 | components/operation/OperationModule.tsx:873 | button | LOCAL_UI |  |
| UI-0824 | components/operation/OperationModule.tsx:873 | onClick | LOCAL_UI |  |
| UI-0825 | components/operation/OperationModule.tsx:881 | select | LOCAL_DRAFT |  |
| UI-0826 | components/operation/OperationModule.tsx:883 | onChange | LOCAL_DRAFT |  |
| UI-0827 | components/operation/OperationModule.tsx:893 | button | LOCAL_UI |  |
| UI-0828 | components/operation/OperationModule.tsx:893 | onClick | LOCAL_UI |  |
| UI-0829 | components/operation/OperationModule.tsx:896 | button | API_CANDIDATE | targetDryRunTokenMove;targetExecuteTokenMove |
| UI-0830 | components/operation/OperationModule.tsx:896 | onClick | API_CANDIDATE | targetDryRunTokenMove;targetExecuteTokenMove |
| UI-0831 | components/operation/OperationModule.tsx:912 | button | LOCAL_UI |  |
| UI-0832 | components/operation/OperationModule.tsx:912 | onClick | LOCAL_UI |  |
| UI-0833 | components/optimize/OptimizeModule.tsx:87 | button | LOCAL_UI |  |
| UI-0834 | components/optimize/OptimizeModule.tsx:88 | onClick | LOCAL_UI |  |
| UI-0835 | components/optimize/OptimizeModule.tsx:98 | button | LOCAL_UI |  |
| UI-0836 | components/optimize/OptimizeModule.tsx:99 | onClick | LOCAL_UI |  |
| UI-0837 | components/optimize/OptimizeModule.tsx:109 | button | LOCAL_UI |  |
| UI-0838 | components/optimize/OptimizeModule.tsx:110 | onClick | LOCAL_UI |  |
| UI-0839 | components/optimize/OptimizeModule.tsx:122 | button | LOCAL_UI |  |
| UI-0840 | components/optimize/OptimizeModule.tsx:123 | onClick | LOCAL_UI |  |
| UI-0841 | components/optimize/OptimizeModule.tsx:134 | button | LOCAL_UI |  |
| UI-0842 | components/optimize/OptimizeModule.tsx:135 | onClick | LOCAL_UI |  |
| UI-0843 | components/optimize/OptimizeModule.tsx:149 | button | LOCAL_UI |  |
| UI-0844 | components/optimize/OptimizeModule.tsx:150 | onClick | LOCAL_UI |  |
| UI-0845 | components/optimize/OptimizeModule.tsx:157 | button | API_CANDIDATE | targetQueryHeatmap |
| UI-0846 | components/optimize/OptimizeModule.tsx:158 | onClick | API_CANDIDATE | targetQueryHeatmap |
| UI-0847 | components/optimize/OptimizeModule.tsx:205 | onClick | LOCAL_UI |  |
| UI-0848 | components/optimize/OptimizeModule.tsx:252 | onClick | LOCAL_UI |  |
| UI-0849 | components/optimize/OptimizeModule.tsx:292 | onClick | LOCAL_UI |  |
| UI-0850 | components/optimize/OptimizeModule.tsx:336 | onClick | LOCAL_UI |  |
| UI-0851 | components/optimize/OptimizeModule.tsx:381 | onNotify | LOCAL_UI |  |
| UI-0852 | components/optimize/OptimizeModule.tsx:384 | onOpenHeatmap | LOCAL_UI |  |
| UI-0853 | components/optimize/OptimizeModule.tsx:694 | onMouseEnter | LOCAL_UI |  |
| UI-0854 | components/optimize/OptimizeModule.tsx:703 | onMouseLeave | LOCAL_UI |  |
| UI-0855 | components/optimize/OptimizeModule.tsx:812 | select | LOCAL_UI |  |
| UI-0856 | components/optimize/OptimizeModule.tsx:814 | onChange | LOCAL_DRAFT |  |
| UI-0857 | components/optimize/OptimizeModule.tsx:825 | select | LOCAL_DRAFT |  |
| UI-0858 | components/optimize/OptimizeModule.tsx:827 | onChange | LOCAL_DRAFT |  |
| UI-0859 | components/optimize/OptimizeModule.tsx:838 | input | LOCAL_DRAFT |  |
| UI-0860 | components/optimize/OptimizeModule.tsx:841 | onChange | LOCAL_DRAFT |  |
| UI-0861 | components/optimize/OptimizeModule.tsx:848 | input | LOCAL_DRAFT |  |
| UI-0862 | components/optimize/OptimizeModule.tsx:851 | onChange | LOCAL_DRAFT |  |
| UI-0863 | components/optimize/OptimizeModule.tsx:856 | button | API_CANDIDATE | targetQueryHeatmap |
| UI-0864 | components/optimize/OptimizeModule.tsx:857 | onClick | API_CANDIDATE | targetQueryHeatmap |
| UI-0865 | components/optimize/ProcessHealthReport.tsx:319 | button | LOCAL_UI |  |
| UI-0866 | components/optimize/ProcessHealthReport.tsx:321 | onClick | LOCAL_UI |  |
| UI-0867 | components/optimize/ProcessHealthReport.tsx:335 | select | LOCAL_UI |  |
| UI-0868 | components/optimize/ProcessHealthReport.tsx:337 | onChange | LOCAL_DRAFT |  |
| UI-0869 | components/optimize/ProcessHealthReport.tsx:349 | button | API_CANDIDATE | targetCreateReportExport |
| UI-0870 | components/optimize/ProcessHealthReport.tsx:350 | onClick | API_CANDIDATE | targetCreateReportExport |
| UI-0871 | components/optimize/ProcessHealthReport.tsx:505 | button | LOCAL_UI |  |
| UI-0872 | components/optimize/ProcessHealthReport.tsx:506 | onClick | LOCAL_UI |  |
| UI-0873 | components/optimize/ProcessHealthReport.tsx:515 | button | LOCAL_UI |  |
| UI-0874 | components/optimize/ProcessHealthReport.tsx:516 | onClick | LOCAL_UI |  |
| UI-0875 | components/optimize/ProcessHealthReport.tsx:525 | button | LOCAL_UI |  |
| UI-0876 | components/optimize/ProcessHealthReport.tsx:526 | onClick | LOCAL_UI |  |
| UI-0877 | components/optimize/ProcessHealthReport.tsx:693 | input | LOCAL_DRAFT |  |
| UI-0878 | components/optimize/ProcessHealthReport.tsx:697 | onChange | LOCAL_DRAFT |  |
| UI-0879 | components/optimize/ProcessHealthReport.tsx:705 | button | LOCAL_UI |  |
| UI-0880 | components/optimize/ProcessHealthReport.tsx:707 | onClick | LOCAL_UI |  |
| UI-0881 | components/optimize/ProcessHealthReport.tsx:794 | button | API_CANDIDATE | targetRetryIncident |
| UI-0882 | components/optimize/ProcessHealthReport.tsx:795 | onClick | API_CANDIDATE | targetRetryIncident |
| UI-0883 | components/optimize/ProcessThroughputDashboard.tsx:298 | select | LOCAL_UI |  |
| UI-0884 | components/optimize/ProcessThroughputDashboard.tsx:300 | onChange | LOCAL_DRAFT |  |
| UI-0885 | components/optimize/ProcessThroughputDashboard.tsx:315 | button | LOCAL_UI |  |
| UI-0886 | components/optimize/ProcessThroughputDashboard.tsx:317 | onClick | LOCAL_UI |  |
| UI-0887 | components/optimize/ProcessThroughputDashboard.tsx:330 | button | API_CANDIDATE | targetCreateReportExport |
| UI-0888 | components/optimize/ProcessThroughputDashboard.tsx:331 | onClick | API_CANDIDATE | targetCreateReportExport |
| UI-0889 | components/optimize/ProcessThroughputDashboard.tsx:340 | button | API_CANDIDATE | targetQueryThroughput |
| UI-0890 | components/optimize/ProcessThroughputDashboard.tsx:341 | onClick | API_CANDIDATE | targetQueryThroughput |
| UI-0891 | components/optimize/ProcessThroughputDashboard.tsx:462 | button | LOCAL_UI |  |
| UI-0892 | components/optimize/ProcessThroughputDashboard.tsx:463 | onClick | LOCAL_UI |  |
| UI-0893 | components/optimize/ProcessThroughputDashboard.tsx:474 | button | LOCAL_UI |  |
| UI-0894 | components/optimize/ProcessThroughputDashboard.tsx:475 | onClick | LOCAL_UI |  |
| UI-0895 | components/optimize/ProcessThroughputDashboard.tsx:486 | button | LOCAL_UI |  |
| UI-0896 | components/optimize/ProcessThroughputDashboard.tsx:487 | onClick | LOCAL_UI |  |
| UI-0897 | components/optimize/ProcessThroughputDashboard.tsx:498 | button | LOCAL_UI |  |
| UI-0898 | components/optimize/ProcessThroughputDashboard.tsx:499 | onClick | LOCAL_UI |  |
| UI-0899 | components/optimize/ProcessThroughputDashboard.tsx:672 | button | LOCAL_UI |  |
| UI-0900 | components/optimize/ProcessThroughputDashboard.tsx:674 | onClick | LOCAL_UI |  |
| UI-0901 | components/optimize/ProcessThroughputDashboard.tsx:915 | input | LOCAL_DRAFT |  |
| UI-0902 | components/optimize/ProcessThroughputDashboard.tsx:919 | onChange | LOCAL_DRAFT |  |
| UI-0903 | components/optimize/ProcessThroughputDashboard.tsx:979 | button | LOCAL_UI |  |
| UI-0904 | components/optimize/ProcessThroughputDashboard.tsx:980 | onClick | LOCAL_UI |  |
| UI-0905 | components/portal/ClientPortalModule.tsx:676 | select | LOCAL_UI |  |
| UI-0906 | components/portal/ClientPortalModule.tsx:678 | onChange | LOCAL_UI |  |
| UI-0907 | components/portal/ClientPortalModule.tsx:691 | button | LOCAL_UI |  |
| UI-0908 | components/portal/ClientPortalModule.tsx:692 | onClick | LOCAL_UI |  |
| UI-0909 | components/portal/ClientPortalModule.tsx:708 | button | LOCAL_UI |  |
| UI-0910 | components/portal/ClientPortalModule.tsx:709 | onClick | LOCAL_UI |  |
| UI-0911 | components/portal/ClientPortalModule.tsx:719 | button | LOCAL_UI |  |
| UI-0912 | components/portal/ClientPortalModule.tsx:720 | onClick | LOCAL_UI |  |
| UI-0913 | components/portal/ClientPortalModule.tsx:732 | button | LOCAL_UI |  |
| UI-0914 | components/portal/ClientPortalModule.tsx:733 | onClick | LOCAL_UI |  |
| UI-0915 | components/portal/ClientPortalModule.tsx:747 | button | LOCAL_UI |  |
| UI-0916 | components/portal/ClientPortalModule.tsx:748 | onClick | LOCAL_UI |  |
| UI-0917 | components/portal/ClientPortalModule.tsx:761 | button | LOCAL_UI |  |
| UI-0918 | components/portal/ClientPortalModule.tsx:762 | onClick | LOCAL_UI |  |
| UI-0919 | components/portal/ClientPortalModule.tsx:773 | button | LOCAL_UI |  |
| UI-0920 | components/portal/ClientPortalModule.tsx:774 | onClick | LOCAL_UI |  |
| UI-0921 | components/portal/ClientPortalModule.tsx:798 | onNavigateTab | LOCAL_UI |  |
| UI-0922 | components/portal/ClientPortalModule.tsx:799 | onFilterStatus | LOCAL_UI |  |
| UI-0923 | components/portal/ClientPortalModule.tsx:803 | onSelectRequest | LOCAL_UI |  |
| UI-0924 | components/portal/ClientPortalModule.tsx:807 | onNotify | LOCAL_UI |  |
| UI-0925 | components/portal/ClientPortalModule.tsx:831 | button | LOCAL_UI |  |
| UI-0926 | components/portal/ClientPortalModule.tsx:832 | onClick | LOCAL_UI |  |
| UI-0927 | components/portal/ClientPortalModule.tsx:838 | button | LOCAL_UI |  |
| UI-0928 | components/portal/ClientPortalModule.tsx:839 | onClick | LOCAL_UI |  |
| UI-0929 | components/portal/ClientPortalModule.tsx:848 | button | LOCAL_UI |  |
| UI-0930 | components/portal/ClientPortalModule.tsx:849 | onClick | LOCAL_UI |  |
| UI-0931 | components/portal/ClientPortalModule.tsx:860 | onClick | LOCAL_UI |  |
| UI-0932 | components/portal/ClientPortalModule.tsx:879 | onClick | LOCAL_UI |  |
| UI-0933 | components/portal/ClientPortalModule.tsx:898 | onClick | LOCAL_UI |  |
| UI-0934 | components/portal/ClientPortalModule.tsx:919 | onClick | LOCAL_UI |  |
| UI-0935 | components/portal/ClientPortalModule.tsx:950 | button | LOCAL_UI |  |
| UI-0936 | components/portal/ClientPortalModule.tsx:951 | onClick | LOCAL_UI |  |
| UI-0937 | components/portal/ClientPortalModule.tsx:977 | onClick | LOCAL_UI |  |
| UI-0938 | components/portal/ClientPortalModule.tsx:1011 | button | LOCAL_UI |  |
| UI-0939 | components/portal/ClientPortalModule.tsx:1012 | onClick | LOCAL_UI |  |
| UI-0940 | components/portal/ClientPortalModule.tsx:1075 | button | LOCAL_UI |  |
| UI-0941 | components/portal/ClientPortalModule.tsx:1076 | onClick | LOCAL_UI |  |
| UI-0942 | components/portal/ClientPortalModule.tsx:1124 | button | LOCAL_UI |  |
| UI-0943 | components/portal/ClientPortalModule.tsx:1125 | onClick | LOCAL_UI |  |
| UI-0944 | components/portal/ClientPortalModule.tsx:1173 | onClick | LOCAL_UI |  |
| UI-0945 | components/portal/ClientPortalModule.tsx:1234 | button | LOCAL_UI |  |
| UI-0946 | components/portal/ClientPortalModule.tsx:1236 | onClick | LOCAL_UI |  |
| UI-0947 | components/portal/ClientPortalModule.tsx:1243 | input | LOCAL_DRAFT |  |
| UI-0948 | components/portal/ClientPortalModule.tsx:1246 | onChange | LOCAL_DRAFT |  |
| UI-0949 | components/portal/ClientPortalModule.tsx:1259 | select | LOCAL_DRAFT |  |
| UI-0950 | components/portal/ClientPortalModule.tsx:1261 | onChange | LOCAL_DRAFT |  |
| UI-0951 | components/portal/ClientPortalModule.tsx:1285 | input | LOCAL_DRAFT |  |
| UI-0952 | components/portal/ClientPortalModule.tsx:1288 | onChange | LOCAL_DRAFT |  |
| UI-0953 | components/portal/ClientPortalModule.tsx:1298 | input | LOCAL_DRAFT |  |
| UI-0954 | components/portal/ClientPortalModule.tsx:1301 | onChange | LOCAL_DRAFT |  |
| UI-0955 | components/portal/ClientPortalModule.tsx:1310 | input | LOCAL_DRAFT |  |
| UI-0956 | components/portal/ClientPortalModule.tsx:1313 | onChange | LOCAL_DRAFT |  |
| UI-0957 | components/portal/ClientPortalModule.tsx:1322 | input | LOCAL_DRAFT |  |
| UI-0958 | components/portal/ClientPortalModule.tsx:1325 | onChange | LOCAL_DRAFT |  |
| UI-0959 | components/portal/ClientPortalModule.tsx:1334 | input | LOCAL_DRAFT |  |
| UI-0960 | components/portal/ClientPortalModule.tsx:1337 | onChange | LOCAL_DRAFT |  |
| UI-0961 | components/portal/ClientPortalModule.tsx:1349 | button | LOCAL_UI |  |
| UI-0962 | components/portal/ClientPortalModule.tsx:1350 | onClick | LOCAL_UI |  |
| UI-0963 | components/portal/ClientPortalModule.tsx:1395 | input | LOCAL_DRAFT |  |
| UI-0964 | components/portal/ClientPortalModule.tsx:1399 | onChange | LOCAL_DRAFT |  |
| UI-0965 | components/portal/ClientPortalModule.tsx:1411 | select | LOCAL_DRAFT |  |
| UI-0966 | components/portal/ClientPortalModule.tsx:1413 | onChange | LOCAL_DRAFT |  |
| UI-0967 | components/portal/ClientPortalModule.tsx:1432 | textarea | LOCAL_DRAFT |  |
| UI-0968 | components/portal/ClientPortalModule.tsx:1435 | onChange | LOCAL_DRAFT |  |
| UI-0969 | components/portal/ClientPortalModule.tsx:1462 | select | LOCAL_DRAFT |  |
| UI-0970 | components/portal/ClientPortalModule.tsx:1464 | onChange | LOCAL_DRAFT |  |
| UI-0971 | components/portal/ClientPortalModule.tsx:1477 | input | LOCAL_DRAFT |  |
| UI-0972 | components/portal/ClientPortalModule.tsx:1480 | onChange | LOCAL_DRAFT |  |
| UI-0973 | components/portal/ClientPortalModule.tsx:1490 | input | LOCAL_DRAFT |  |
| UI-0974 | components/portal/ClientPortalModule.tsx:1493 | onChange | LOCAL_DRAFT |  |
| UI-0975 | components/portal/ClientPortalModule.tsx:1502 | input | LOCAL_DRAFT |  |
| UI-0976 | components/portal/ClientPortalModule.tsx:1505 | onChange | LOCAL_DRAFT |  |
| UI-0977 | components/portal/ClientPortalModule.tsx:1514 | input | LOCAL_DRAFT |  |
| UI-0978 | components/portal/ClientPortalModule.tsx:1517 | onChange | LOCAL_DRAFT |  |
| UI-0979 | components/portal/ClientPortalModule.tsx:1529 | input | LOCAL_DRAFT |  |
| UI-0980 | components/portal/ClientPortalModule.tsx:1533 | onChange | LOCAL_DRAFT |  |
| UI-0981 | components/portal/ClientPortalModule.tsx:1542 | select | LOCAL_DRAFT |  |
| UI-0982 | components/portal/ClientPortalModule.tsx:1544 | onChange | LOCAL_DRAFT |  |
| UI-0983 | components/portal/ClientPortalModule.tsx:1557 | input | LOCAL_DRAFT |  |
| UI-0984 | components/portal/ClientPortalModule.tsx:1561 | onChange | LOCAL_DRAFT |  |
| UI-0985 | components/portal/ClientPortalModule.tsx:1570 | input | LOCAL_DRAFT |  |
| UI-0986 | components/portal/ClientPortalModule.tsx:1574 | onChange | LOCAL_DRAFT |  |
| UI-0987 | components/portal/ClientPortalModule.tsx:1583 | input | LOCAL_DRAFT |  |
| UI-0988 | components/portal/ClientPortalModule.tsx:1586 | onChange | LOCAL_DRAFT |  |
| UI-0989 | components/portal/ClientPortalModule.tsx:1613 | button | LOCAL_UI |  |
| UI-0990 | components/portal/ClientPortalModule.tsx:1615 | onClick | LOCAL_UI |  |
| UI-0991 | components/portal/ClientPortalModule.tsx:1620 | button | LOCAL_UI |  |
| UI-0992 | components/portal/ClientPortalModule.tsx:1622 | onClick | LOCAL_UI |  |
| UI-0993 | components/portal/ClientPortalModule.tsx:1638 | button | LOCAL_UI |  |
| UI-0994 | components/portal/ClientPortalModule.tsx:1639 | onClick | LOCAL_UI |  |
| UI-0995 | components/portal/ClientPortalModule.tsx:1651 | input | LOCAL_DRAFT |  |
| UI-0996 | components/portal/ClientPortalModule.tsx:1655 | onChange | LOCAL_DRAFT |  |
| UI-0997 | components/portal/ClientPortalModule.tsx:1664 | select | LOCAL_DRAFT |  |
| UI-0998 | components/portal/ClientPortalModule.tsx:1666 | onChange | LOCAL_DRAFT |  |
| UI-0999 | components/portal/ClientPortalModule.tsx:1680 | input | LOCAL_DRAFT |  |
| UI-1000 | components/portal/ClientPortalModule.tsx:1684 | onChange | LOCAL_DRAFT |  |
| UI-1001 | components/portal/ClientPortalModule.tsx:1693 | input | LOCAL_DRAFT |  |
| UI-1002 | components/portal/ClientPortalModule.tsx:1697 | onChange | LOCAL_DRAFT |  |
| UI-1003 | components/portal/ClientPortalModule.tsx:1704 | button | LOCAL_UI |  |
| UI-1004 | components/portal/ClientPortalModule.tsx:1706 | onClick | LOCAL_UI |  |
| UI-1005 | components/portal/ClientPortalModule.tsx:1711 | button | LOCAL_UI |  |
| UI-1006 | components/portal/ClientPortalModule.tsx:1713 | onClick | LOCAL_UI |  |
| UI-1007 | components/portal/ClientPortalModule.tsx:1746 | input | LOCAL_DRAFT |  |
| UI-1008 | components/portal/ClientPortalModule.tsx:1749 | onChange | LOCAL_DRAFT |  |
| UI-1009 | components/portal/ClientPortalModule.tsx:1760 | button | LOCAL_UI |  |
| UI-1010 | components/portal/ClientPortalModule.tsx:1762 | onClick | LOCAL_UI |  |
| UI-1011 | components/portal/ClientPortalModule.tsx:1778 | button | LOCAL_UI |  |
| UI-1012 | components/portal/ClientPortalModule.tsx:1779 | onClick | LOCAL_UI |  |
| UI-1013 | components/portal/ClientPortalModule.tsx:1786 | button | LOCAL_UI |  |
| UI-1014 | components/portal/ClientPortalModule.tsx:1787 | onClick | LOCAL_UI |  |
| UI-1015 | components/portal/ClientPortalModule.tsx:1885 | button | API_CANDIDATE | targetAttachCaseDocument;targetCreateDocumentUpload;targetFinalizeDocumentUpload |
| UI-1016 | components/portal/ClientPortalModule.tsx:1887 | onClick | API_CANDIDATE | targetAttachCaseDocument;targetCreateDocumentUpload;targetFinalizeDocumentUpload |
| UI-1017 | components/portal/ClientPortalModule.tsx:1893 | button | API_CANDIDATE | targetAttachCaseDocument;targetCreateDocumentUpload;targetFinalizeDocumentUpload |
| UI-1018 | components/portal/ClientPortalModule.tsx:1895 | onClick | API_CANDIDATE | targetAttachCaseDocument;targetCreateDocumentUpload;targetFinalizeDocumentUpload |
| UI-1019 | components/portal/ClientPortalModule.tsx:1929 | button | API_CANDIDATE | targetAttachCaseDocument;targetCreateDocumentUpload;targetFinalizeDocumentUpload |
| UI-1020 | components/portal/ClientPortalModule.tsx:1931 | onClick | API_CANDIDATE | targetAttachCaseDocument;targetCreateDocumentUpload;targetFinalizeDocumentUpload |
| UI-1021 | components/portal/ClientPortalModule.tsx:1964 | button | API_CANDIDATE | targetAttachCaseDocument;targetCreateDocumentUpload;targetFinalizeDocumentUpload |
| UI-1022 | components/portal/ClientPortalModule.tsx:1966 | onClick | API_CANDIDATE | targetAttachCaseDocument;targetCreateDocumentUpload;targetFinalizeDocumentUpload |
| UI-1023 | components/portal/ClientPortalModule.tsx:1979 | button | LOCAL_UI |  |
| UI-1024 | components/portal/ClientPortalModule.tsx:1980 | onClick | LOCAL_UI |  |
| UI-1025 | components/portal/ClientPortalModule.tsx:1987 | button | LOCAL_UI |  |
| UI-1026 | components/portal/ClientPortalModule.tsx:1988 | onClick | LOCAL_UI |  |
| UI-1027 | components/portal/ClientPortalModule.tsx:2061 | button | LOCAL_UI |  |
| UI-1028 | components/portal/ClientPortalModule.tsx:2063 | onClick | LOCAL_UI |  |
| UI-1029 | components/portal/ClientPortalModule.tsx:2070 | button | LOCAL_UI |  |
| UI-1030 | components/portal/ClientPortalModule.tsx:2072 | onClick | LOCAL_UI |  |
| UI-1031 | components/portal/ClientPortalModule.tsx:2081 | button | LOCAL_UI |  |
| UI-1032 | components/portal/ClientPortalModule.tsx:2083 | onClick | LOCAL_UI |  |
| UI-1033 | components/portal/ClientPortalModule.tsx:2157 | input | LOCAL_DRAFT |  |
| UI-1034 | components/portal/ClientPortalModule.tsx:2161 | onChange | LOCAL_DRAFT |  |
| UI-1035 | components/portal/ClientPortalModule.tsx:2177 | button | LOCAL_UI |  |
| UI-1036 | components/portal/ClientPortalModule.tsx:2178 | onClick | LOCAL_UI |  |
| UI-1037 | components/portal/ClientPortalModule.tsx:2185 | button | API_CANDIDATE | targetCreateCustomerCase;targetSubmitCustomerCase |
| UI-1038 | components/portal/ClientPortalModule.tsx:2187 | onClick | API_CANDIDATE | targetCreateCustomerCase;targetSubmitCustomerCase |
| UI-1039 | components/portal/ClientPortalModule.tsx:2213 | button | LOCAL_UI |  |
| UI-1040 | components/portal/ClientPortalModule.tsx:2214 | onClick | LOCAL_UI |  |
| UI-1041 | components/portal/ClientPortalModule.tsx:2226 | input | LOCAL_UI |  |
| UI-1042 | components/portal/ClientPortalModule.tsx:2230 | onChange | LOCAL_DRAFT |  |
| UI-1043 | components/portal/ClientPortalModule.tsx:2238 | button | LOCAL_UI |  |
| UI-1044 | components/portal/ClientPortalModule.tsx:2240 | onClick | LOCAL_UI |  |
| UI-1045 | components/portal/ClientPortalModule.tsx:2261 | onClick | LOCAL_UI |  |
| UI-1046 | components/portal/ClientPortalModule.tsx:2286 | button | LOCAL_UI |  |
| UI-1047 | components/portal/ClientPortalModule.tsx:2288 | onClick | LOCAL_UI |  |
| UI-1048 | components/portal/ClientPortalModule.tsx:2356 | button | LOCAL_UI |  |
| UI-1049 | components/portal/ClientPortalModule.tsx:2357 | onClick | LOCAL_UI |  |
| UI-1050 | components/portal/ClientPortalModule.tsx:2381 | button | LOCAL_UI |  |
| UI-1051 | components/portal/ClientPortalModule.tsx:2382 | onClick | LOCAL_UI |  |
| UI-1052 | components/portal/ClientPortalModule.tsx:2407 | button | LOCAL_UI |  |
| UI-1053 | components/portal/ClientPortalModule.tsx:2408 | onClick | LOCAL_UI |  |
| UI-1054 | components/portal/ClientPortalModule.tsx:2419 | button | LOCAL_UI |  |
| UI-1055 | components/portal/ClientPortalModule.tsx:2420 | onClick | LOCAL_UI |  |
| UI-1056 | components/portal/ClientPortalModule.tsx:2431 | button | LOCAL_UI |  |
| UI-1057 | components/portal/ClientPortalModule.tsx:2432 | onClick | LOCAL_UI |  |
| UI-1058 | components/portal/ClientPortalModule.tsx:2443 | button | LOCAL_UI |  |
| UI-1059 | components/portal/ClientPortalModule.tsx:2444 | onClick | LOCAL_UI |  |
| UI-1060 | components/portal/ClientPortalModule.tsx:2471 | button | LOCAL_UI |  |
| UI-1061 | components/portal/ClientPortalModule.tsx:2472 | onClick | LOCAL_UI |  |
| UI-1062 | components/portal/ClientPortalModule.tsx:2490 | onNotify | LOCAL_UI |  |
| UI-1063 | components/portal/ClientPortalModule.tsx:2491 | onOpenAdminConsole | LOCAL_UI |  |
| UI-1064 | components/portal/ClientPortalModule.tsx:2492 | onUploadAdditionalDoc | LOCAL_UI |  |
| UI-1065 | components/portal/ClientPortalModule.tsx:2503 | button | LOCAL_UI |  |
| UI-1066 | components/portal/ClientPortalModule.tsx:2504 | onClick | LOCAL_UI |  |
| UI-1067 | components/portal/ClientPortalModule.tsx:2537 | button | API_CANDIDATE | targetAuthorizeDocumentDownload |
| UI-1068 | components/portal/ClientPortalModule.tsx:2538 | onClick | API_CANDIDATE | targetAuthorizeDocumentDownload |
| UI-1069 | components/portal/ClientPortalModule.tsx:2590 | input | API_CANDIDATE | targetAddCaseComment |
| UI-1070 | components/portal/ClientPortalModule.tsx:2593 | onChange | LOCAL_DRAFT |  |
| UI-1071 | components/portal/ClientPortalModule.tsx:2594 | onKeyDown | API_CANDIDATE | targetAddCaseComment |
| UI-1072 | components/portal/ClientPortalModule.tsx:2598 | button | API_CANDIDATE | targetAddCaseComment |
| UI-1073 | components/portal/ClientPortalModule.tsx:2599 | onClick | API_CANDIDATE | targetAddCaseComment |
| UI-1074 | components/portal/ClientPortalModule.tsx:2740 | button | LOCAL_UI |  |
| UI-1075 | components/portal/ClientPortalModule.tsx:2741 | onClick | LOCAL_UI |  |
| UI-1076 | components/portal/ClientPortalModule.tsx:2751 | button | LOCAL_UI |  |
| UI-1077 | components/portal/ClientPortalModule.tsx:2752 | onClick | LOCAL_UI |  |
| UI-1078 | components/portal/ClientPortalModule.tsx:2790 | input | API_CANDIDATE | targetQueryCustomerCaseLookup |
| UI-1079 | components/portal/ClientPortalModule.tsx:2793 | onChange | LOCAL_DRAFT |  |
| UI-1080 | components/portal/ClientPortalModule.tsx:2794 | onKeyDown | API_CANDIDATE | targetQueryCustomerCaseLookup |
| UI-1081 | components/portal/ClientPortalModule.tsx:2798 | button | API_CANDIDATE | targetQueryCustomerCaseLookup |
| UI-1082 | components/portal/ClientPortalModule.tsx:2799 | onClick | API_CANDIDATE | targetQueryCustomerCaseLookup |
| UI-1083 | components/portal/ClientPortalModule.tsx:2841 | button | LOCAL_UI |  |
| UI-1084 | components/portal/ClientPortalModule.tsx:2842 | onClick | LOCAL_UI |  |
| UI-1085 | components/portal/ClientPortalModule.tsx:2877 | onNotify | LOCAL_UI |  |
| UI-1086 | components/portal/ClientPortalModule.tsx:2878 | onOpenAdminConsole | LOCAL_UI |  |
| UI-1087 | components/portal/ClientPortalModule.tsx:2879 | onUploadAdditionalDoc | LOCAL_UI |  |
| UI-1088 | components/portal/ClientPortalModule.tsx:2921 | button | API_CANDIDATE | targetAuthorizeDocumentDownload |
| UI-1089 | components/portal/ClientPortalModule.tsx:2922 | onClick | API_CANDIDATE | targetAuthorizeDocumentDownload |
| UI-1090 | components/portal/ClientPortalModule.tsx:2956 | button | LOCAL_UI |  |
| UI-1091 | components/portal/ClientPortalModule.tsx:2957 | onClick | LOCAL_UI |  |
| UI-1092 | components/portal/ClientPortalModule.tsx:2973 | input | LOCAL_DRAFT |  |
| UI-1093 | components/portal/ClientPortalModule.tsx:2976 | onChange | LOCAL_DRAFT |  |
| UI-1094 | components/portal/ClientPortalModule.tsx:2985 | textarea | LOCAL_DRAFT |  |
| UI-1095 | components/portal/ClientPortalModule.tsx:2988 | onChange | LOCAL_DRAFT |  |
| UI-1096 | components/portal/ClientPortalModule.tsx:2995 | button | LOCAL_UI |  |
| UI-1097 | components/portal/ClientPortalModule.tsx:2996 | onClick | LOCAL_UI |  |
| UI-1098 | components/portal/ClientPortalModule.tsx:3001 | button | API_CANDIDATE | targetAttachCaseDocument;targetCreateDocumentUpload;targetFinalizeDocumentUpload |
| UI-1099 | components/portal/ClientPortalModule.tsx:3002 | onClick | API_CANDIDATE | targetAttachCaseDocument;targetCreateDocumentUpload;targetFinalizeDocumentUpload |
| UI-1100 | components/portal/ClientPortalModule.tsx:3032 | button | LOCAL_UI |  |
| UI-1101 | components/portal/ClientPortalModule.tsx:3033 | onClick | LOCAL_UI |  |
| UI-1102 | components/portal/ClientPortalModule.tsx:3046 | button | LOCAL_UI |  |
| UI-1103 | components/portal/ClientPortalModule.tsx:3048 | onClick | LOCAL_UI |  |
| UI-1104 | components/portal/ClientPortalModule.tsx:3059 | button | LOCAL_UI |  |
| UI-1105 | components/portal/ClientPortalModule.tsx:3061 | onClick | LOCAL_UI |  |
| UI-1106 | components/portal/ClientPortalModule.tsx:3072 | button | LOCAL_UI |  |
| UI-1107 | components/portal/ClientPortalModule.tsx:3074 | onClick | LOCAL_UI |  |
| UI-1108 | components/portal/ClientPortalModule.tsx:3091 | textarea | LOCAL_DRAFT |  |
| UI-1109 | components/portal/ClientPortalModule.tsx:3094 | onChange | LOCAL_DRAFT |  |
| UI-1110 | components/portal/ClientPortalModule.tsx:3101 | button | LOCAL_UI |  |
| UI-1111 | components/portal/ClientPortalModule.tsx:3102 | onClick | LOCAL_UI |  |
| UI-1112 | components/portal/ClientPortalModule.tsx:3107 | button | API_CANDIDATE | completeWorkItem;targetSubmitWorkItemForm |
| UI-1113 | components/portal/ClientPortalModule.tsx:3108 | onClick | API_CANDIDATE | completeWorkItem;targetSubmitWorkItemForm |
| UI-1114 | components/portal/EmbeddedProcessViewer.tsx:440 | button | LOCAL_UI |  |
| UI-1115 | components/portal/EmbeddedProcessViewer.tsx:441 | onClick | LOCAL_UI |  |
| UI-1116 | components/portal/EmbeddedProcessViewer.tsx:450 | button | LOCAL_UI |  |
| UI-1117 | components/portal/EmbeddedProcessViewer.tsx:451 | onClick | LOCAL_UI |  |
| UI-1118 | components/portal/EmbeddedProcessViewer.tsx:466 | button | LOCAL_UI |  |
| UI-1119 | components/portal/EmbeddedProcessViewer.tsx:467 | onClick | LOCAL_UI |  |
| UI-1120 | components/portal/EmbeddedProcessViewer.tsx:480 | button | LOCAL_UI |  |
| UI-1121 | components/portal/EmbeddedProcessViewer.tsx:481 | onClick | LOCAL_UI |  |
| UI-1122 | components/portal/EmbeddedProcessViewer.tsx:512 | button | LOCAL_UI |  |
| UI-1123 | components/portal/EmbeddedProcessViewer.tsx:513 | onClick | LOCAL_UI |  |
| UI-1124 | components/portal/EmbeddedProcessViewer.tsx:524 | button | LOCAL_UI |  |
| UI-1125 | components/portal/EmbeddedProcessViewer.tsx:525 | onClick | LOCAL_UI |  |
| UI-1126 | components/portal/EmbeddedProcessViewer.tsx:541 | button | LOCAL_UI |  |
| UI-1127 | components/portal/EmbeddedProcessViewer.tsx:542 | onClick | LOCAL_UI |  |
| UI-1128 | components/portal/EmbeddedProcessViewer.tsx:556 | button | LOCAL_UI |  |
| UI-1129 | components/portal/EmbeddedProcessViewer.tsx:557 | onClick | LOCAL_UI |  |
| UI-1130 | components/portal/EmbeddedProcessViewer.tsx:573 | button | LOCAL_UI |  |
| UI-1131 | components/portal/EmbeddedProcessViewer.tsx:574 | onClick | LOCAL_UI |  |
| UI-1132 | components/portal/EmbeddedProcessViewer.tsx:587 | button | LOCAL_UI |  |
| UI-1133 | components/portal/EmbeddedProcessViewer.tsx:588 | onClick | LOCAL_UI |  |
| UI-1134 | components/portal/EmbeddedProcessViewer.tsx:606 | button | LOCAL_UI |  |
| UI-1135 | components/portal/EmbeddedProcessViewer.tsx:607 | onClick | LOCAL_UI |  |
| UI-1136 | components/portal/EmbeddedProcessViewer.tsx:616 | button | LOCAL_UI |  |
| UI-1137 | components/portal/EmbeddedProcessViewer.tsx:617 | onClick | LOCAL_UI |  |
| UI-1138 | components/portal/EmbeddedProcessViewer.tsx:623 | button | LOCAL_UI |  |
| UI-1139 | components/portal/EmbeddedProcessViewer.tsx:624 | onClick | LOCAL_UI |  |
| UI-1140 | components/portal/EmbeddedProcessViewer.tsx:648 | button | API_CANDIDATE | targetAttachCaseDocument;targetCreateDocumentUpload;targetFinalizeDocumentUpload |
| UI-1141 | components/portal/EmbeddedProcessViewer.tsx:649 | onClick | API_CANDIDATE | targetAttachCaseDocument;targetCreateDocumentUpload;targetFinalizeDocumentUpload |
| UI-1142 | components/portal/EmbeddedProcessViewer.tsx:814 | onClick | LOCAL_UI |  |
| UI-1143 | components/portal/EmbeddedProcessViewer.tsx:1012 | button | LOCAL_UI |  |
| UI-1144 | components/portal/EmbeddedProcessViewer.tsx:1013 | onClick | LOCAL_UI |  |
| UI-1145 | components/portal/EmbeddedProcessViewer.tsx:1072 | button | LOCAL_UI |  |
| UI-1146 | components/portal/EmbeddedProcessViewer.tsx:1073 | onClick | LOCAL_UI |  |
| UI-1147 | components/portal/EmbeddedProcessViewer.tsx:1105 | button | LOCAL_UI |  |
| UI-1148 | components/portal/EmbeddedProcessViewer.tsx:1106 | onClick | LOCAL_UI |  |
| UI-1149 | components/portal/EmbeddedProcessViewer.tsx:1113 | button | LOCAL_UI |  |
| UI-1150 | components/portal/EmbeddedProcessViewer.tsx:1114 | onClick | LOCAL_UI |  |
| UI-1151 | components/portal/EmbeddedProcessViewer.tsx:1123 | button | LOCAL_UI |  |
| UI-1152 | components/portal/EmbeddedProcessViewer.tsx:1124 | onClick | LOCAL_UI |  |
| UI-1153 | components/portal/EmbeddedProcessViewer.tsx:1144 | input | LOCAL_DRAFT |  |
| UI-1154 | components/portal/EmbeddedProcessViewer.tsx:1148 | onChange | LOCAL_DRAFT |  |
| UI-1155 | components/portal/EmbeddedProcessViewer.tsx:1162 | button | LOCAL_UI |  |
| UI-1156 | components/portal/EmbeddedProcessViewer.tsx:1164 | onClick | LOCAL_UI |  |
| UI-1157 | components/portal/EmbeddedProcessViewer.tsx:1216 | button | LOCAL_UI |  |
| UI-1158 | components/portal/EmbeddedProcessViewer.tsx:1217 | onClick | LOCAL_UI |  |
| UI-1159 | components/portal/PortalMetricsDashboard.tsx:307 | select | LOCAL_UI |  |
| UI-1160 | components/portal/PortalMetricsDashboard.tsx:309 | onChange | LOCAL_DRAFT |  |
| UI-1161 | components/portal/PortalMetricsDashboard.tsx:323 | button | LOCAL_UI |  |
| UI-1162 | components/portal/PortalMetricsDashboard.tsx:325 | onClick | LOCAL_UI |  |
| UI-1163 | components/portal/PortalMetricsDashboard.tsx:338 | button | API_CANDIDATE | targetQueryPortalMetrics |
| UI-1164 | components/portal/PortalMetricsDashboard.tsx:339 | onClick | API_CANDIDATE | targetQueryPortalMetrics |
| UI-1165 | components/portal/PortalMetricsDashboard.tsx:354 | onClick | LOCAL_UI |  |
| UI-1166 | components/portal/PortalMetricsDashboard.tsx:412 | onClick | LOCAL_UI |  |
| UI-1167 | components/portal/PortalMetricsDashboard.tsx:728 | button | LOCAL_UI |  |
| UI-1168 | components/portal/PortalMetricsDashboard.tsx:729 | onClick | LOCAL_UI |  |
| UI-1169 | components/task-management/AccessAuditLog.tsx:238 | input | LOCAL_UI |  |
| UI-1170 | components/task-management/AccessAuditLog.tsx:242 | onChange | LOCAL_DRAFT |  |
| UI-1171 | components/task-management/AccessAuditLog.tsx:250 | button | LOCAL_UI |  |
| UI-1172 | components/task-management/AccessAuditLog.tsx:251 | onClick | LOCAL_UI |  |
| UI-1173 | components/task-management/AccessAuditLog.tsx:264 | select | LOCAL_UI |  |
| UI-1174 | components/task-management/AccessAuditLog.tsx:266 | onChange | LOCAL_DRAFT |  |
| UI-1175 | components/task-management/AccessAuditLog.tsx:281 | select | LOCAL_UI |  |
| UI-1176 | components/task-management/AccessAuditLog.tsx:283 | onChange | LOCAL_DRAFT |  |
| UI-1177 | components/task-management/AccessAuditLog.tsx:301 | select | LOCAL_UI |  |
| UI-1178 | components/task-management/AccessAuditLog.tsx:303 | onChange | LOCAL_DRAFT |  |
| UI-1179 | components/task-management/AccessAuditLog.tsx:318 | button | LOCAL_UI |  |
| UI-1180 | components/task-management/AccessAuditLog.tsx:319 | onClick | LOCAL_UI |  |
| UI-1181 | components/task-management/AccessAuditLog.tsx:332 | button | API_CANDIDATE | targetCreateAccessAuditExport |
| UI-1182 | components/task-management/AccessAuditLog.tsx:333 | onClick | API_CANDIDATE | targetCreateAccessAuditExport |
| UI-1183 | components/task-management/AccessAuditLog.tsx:367 | button | LOCAL_UI |  |
| UI-1184 | components/task-management/AccessAuditLog.tsx:368 | onClick | LOCAL_UI |  |
| UI-1185 | components/task-management/AccessAuditLog.tsx:477 | button | LOCAL_UI |  |
| UI-1186 | components/task-management/AccessAuditLog.tsx:478 | onClick | LOCAL_UI |  |
| UI-1187 | components/task-management/AccessAuditLog.tsx:487 | button | LOCAL_UI |  |
| UI-1188 | components/task-management/AccessAuditLog.tsx:488 | onClick | LOCAL_UI |  |
| UI-1189 | components/task-management/TaskManagementModule.tsx:346 | input | LOCAL_UI |  |
| UI-1190 | components/task-management/TaskManagementModule.tsx:350 | onChange | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1191 | components/task-management/TaskManagementModule.tsx:366 | button | LOCAL_UI |  |
| UI-1192 | components/task-management/TaskManagementModule.tsx:368 | onClick | LOCAL_UI |  |
| UI-1193 | components/task-management/TaskManagementModule.tsx:431 | button | LOCAL_UI |  |
| UI-1194 | components/task-management/TaskManagementModule.tsx:432 | onClick | LOCAL_UI |  |
| UI-1195 | components/task-management/TaskManagementModule.tsx:442 | button | LOCAL_UI |  |
| UI-1196 | components/task-management/TaskManagementModule.tsx:443 | onClick | LOCAL_UI |  |
| UI-1197 | components/task-management/TaskManagementModule.tsx:458 | button | API_CANDIDATE | targetCreateAccessAuditExport |
| UI-1198 | components/task-management/TaskManagementModule.tsx:459 | onClick | API_CANDIDATE | targetCreateAccessAuditExport |
| UI-1199 | components/task-management/TaskManagementModule.tsx:470 | button | LOCAL_UI |  |
| UI-1200 | components/task-management/TaskManagementModule.tsx:471 | onClick | LOCAL_UI |  |
| UI-1201 | components/task-management/TaskManagementModule.tsx:483 | button | LOCAL_UI |  |
| UI-1202 | components/task-management/TaskManagementModule.tsx:484 | onClick | LOCAL_UI |  |
| UI-1203 | components/task-management/TaskManagementModule.tsx:498 | button | LOCAL_UI |  |
| UI-1204 | components/task-management/TaskManagementModule.tsx:499 | onClick | LOCAL_UI |  |
| UI-1205 | components/task-management/TaskManagementModule.tsx:513 | button | LOCAL_UI |  |
| UI-1206 | components/task-management/TaskManagementModule.tsx:514 | onClick | LOCAL_UI |  |
| UI-1207 | components/task-management/TaskManagementModule.tsx:528 | button | LOCAL_UI |  |
| UI-1208 | components/task-management/TaskManagementModule.tsx:529 | onClick | LOCAL_UI |  |
| UI-1209 | components/task-management/TaskManagementModule.tsx:593 | button | LOCAL_UI |  |
| UI-1210 | components/task-management/TaskManagementModule.tsx:594 | onClick | LOCAL_UI |  |
| UI-1211 | components/task-management/TaskManagementModule.tsx:637 | button | API_CANDIDATE | targetPublishPolicy;targetUpdatePolicy |
| UI-1212 | components/task-management/TaskManagementModule.tsx:639 | onClick | API_CANDIDATE | targetPublishPolicy;targetUpdatePolicy |
| UI-1213 | components/task-management/TaskManagementModule.tsx:685 | onClick | LOCAL_UI |  |
| UI-1214 | components/task-management/TaskManagementModule.tsx:744 | button | LOCAL_UI |  |
| UI-1215 | components/task-management/TaskManagementModule.tsx:745 | onClick | LOCAL_UI |  |
| UI-1216 | components/task-management/TaskManagementModule.tsx:791 | button | LOCAL_UI |  |
| UI-1217 | components/task-management/TaskManagementModule.tsx:792 | onClick | LOCAL_UI |  |
| UI-1218 | components/task-management/TaskManagementModule.tsx:798 | button | LOCAL_UI |  |
| UI-1219 | components/task-management/TaskManagementModule.tsx:799 | onClick | LOCAL_UI |  |
| UI-1220 | components/task-management/TaskManagementModule.tsx:864 | button | LOCAL_UI |  |
| UI-1221 | components/task-management/TaskManagementModule.tsx:865 | onClick | LOCAL_UI |  |
| UI-1222 | components/task-management/TaskManagementModule.tsx:883 | onExportCSV | API_CANDIDATE | targetCreateAccessAuditExport |
| UI-1223 | components/task-management/TaskManagementModule.tsx:894 | onClick | LOCAL_UI |  |
| UI-1224 | components/task-management/TaskManagementModule.tsx:913 | button | LOCAL_UI |  |
| UI-1225 | components/task-management/TaskManagementModule.tsx:914 | onClick | LOCAL_UI |  |
| UI-1226 | components/task-management/TaskManagementModule.tsx:931 | input | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1227 | components/task-management/TaskManagementModule.tsx:935 | onChange | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1228 | components/task-management/TaskManagementModule.tsx:946 | button | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1229 | components/task-management/TaskManagementModule.tsx:949 | onClick | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1230 | components/task-management/TaskManagementModule.tsx:983 | button | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1231 | components/task-management/TaskManagementModule.tsx:984 | onClick | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1232 | components/task-management/TaskManagementModule.tsx:994 | input | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1233 | components/task-management/TaskManagementModule.tsx:997 | onChange | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1234 | components/task-management/TaskManagementModule.tsx:1004 | input | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1235 | components/task-management/TaskManagementModule.tsx:1007 | onChange | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1236 | components/task-management/TaskManagementModule.tsx:1014 | input | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1237 | components/task-management/TaskManagementModule.tsx:1017 | onChange | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1238 | components/task-management/TaskManagementModule.tsx:1024 | input | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1239 | components/task-management/TaskManagementModule.tsx:1027 | onChange | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1240 | components/task-management/TaskManagementModule.tsx:1055 | input | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1241 | components/task-management/TaskManagementModule.tsx:1058 | onChange | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1242 | components/task-management/TaskManagementModule.tsx:1076 | onClick | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1243 | components/task-management/TaskManagementModule.tsx:1080 | onClick | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1244 | components/task-management/TaskManagementModule.tsx:1090 | button | LOCAL_UI |  |
| UI-1245 | components/task-management/TaskManagementModule.tsx:1091 | onClick | LOCAL_UI |  |
| UI-1246 | components/task-management/TaskManagementModule.tsx:1096 | button | API_CANDIDATE | targetPublishPolicy;targetUpdatePolicy |
| UI-1247 | components/task-management/TaskManagementModule.tsx:1097 | onClick | API_CANDIDATE | targetPublishPolicy;targetUpdatePolicy |
| UI-1248 | components/task-management/TaskManagementModule.tsx:1117 | button | LOCAL_UI |  |
| UI-1249 | components/task-management/TaskManagementModule.tsx:1117 | onClick | LOCAL_UI |  |
| UI-1250 | components/task-management/TaskManagementModule.tsx:1122 | input | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1251 | components/task-management/TaskManagementModule.tsx:1125 | onChange | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1252 | components/task-management/TaskManagementModule.tsx:1132 | input | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1253 | components/task-management/TaskManagementModule.tsx:1135 | onChange | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1254 | components/task-management/TaskManagementModule.tsx:1144 | button | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1255 | components/task-management/TaskManagementModule.tsx:1147 | onClick | LOCAL_DRAFT | targetPublishPolicy;targetUpdatePolicy |
| UI-1256 | components/task-management/TaskManagementModule.tsx:1163 | button | LOCAL_UI |  |
| UI-1257 | components/task-management/TaskManagementModule.tsx:1163 | onClick | LOCAL_UI |  |
| UI-1258 | components/task-management/TaskManagementModule.tsx:1166 | button | API_CANDIDATE | targetCreateQueue |
| UI-1259 | components/task-management/TaskManagementModule.tsx:1166 | onClick | API_CANDIDATE | targetCreateQueue |
| UI-1260 | components/tasklist/TasklistModule.tsx:251 | button | LOCAL_UI |  |
| UI-1261 | components/tasklist/TasklistModule.tsx:252 | onClick | LOCAL_UI |  |
| UI-1262 | components/tasklist/TasklistModule.tsx:261 | button | LOCAL_UI |  |
| UI-1263 | components/tasklist/TasklistModule.tsx:262 | onClick | LOCAL_UI |  |
| UI-1264 | components/tasklist/TasklistModule.tsx:271 | button | LOCAL_UI |  |
| UI-1265 | components/tasklist/TasklistModule.tsx:272 | onClick | LOCAL_UI |  |
| UI-1266 | components/tasklist/TasklistModule.tsx:285 | button | LOCAL_UI |  |
| UI-1267 | components/tasklist/TasklistModule.tsx:286 | onClick | LOCAL_UI |  |
| UI-1268 | components/tasklist/TasklistModule.tsx:297 | button | LOCAL_UI |  |
| UI-1269 | components/tasklist/TasklistModule.tsx:298 | onClick | LOCAL_UI |  |
| UI-1270 | components/tasklist/TasklistModule.tsx:322 | input | LOCAL_UI |  |
| UI-1271 | components/tasklist/TasklistModule.tsx:326 | onChange | LOCAL_DRAFT |  |
| UI-1272 | components/tasklist/TasklistModule.tsx:331 | button | LOCAL_UI |  |
| UI-1273 | components/tasklist/TasklistModule.tsx:332 | onClick | LOCAL_UI |  |
| UI-1274 | components/tasklist/TasklistModule.tsx:349 | button | LOCAL_UI |  |
| UI-1275 | components/tasklist/TasklistModule.tsx:351 | onClick | LOCAL_UI |  |
| UI-1276 | components/tasklist/TasklistModule.tsx:375 | select | LOCAL_UI |  |
| UI-1277 | components/tasklist/TasklistModule.tsx:377 | onChange | LOCAL_DRAFT |  |
| UI-1278 | components/tasklist/TasklistModule.tsx:396 | input | LOCAL_DRAFT |  |
| UI-1279 | components/tasklist/TasklistModule.tsx:399 | onChange | LOCAL_DRAFT |  |
| UI-1280 | components/tasklist/TasklistModule.tsx:428 | onClick | LOCAL_UI |  |
| UI-1281 | components/tasklist/TasklistModule.tsx:438 | onClick | LOCAL_UI |  |
| UI-1282 | components/tasklist/TasklistModule.tsx:439 | input | LOCAL_DRAFT |  |
| UI-1283 | components/tasklist/TasklistModule.tsx:442 | onChange | LOCAL_DRAFT |  |
| UI-1284 | components/tasklist/TasklistModule.tsx:500 | onClick | LOCAL_UI |  |
| UI-1285 | components/tasklist/TasklistModule.tsx:502 | button | LOCAL_UI |  |
| UI-1286 | components/tasklist/TasklistModule.tsx:503 | onClick | LOCAL_UI |  |
| UI-1287 | components/tasklist/TasklistModule.tsx:513 | button | API_CANDIDATE | unclaimWorkItem |
| UI-1288 | components/tasklist/TasklistModule.tsx:514 | onClick | API_CANDIDATE | unclaimWorkItem |
| UI-1289 | components/tasklist/TasklistModule.tsx:521 | button | API_CANDIDATE | claimWorkItem |
| UI-1290 | components/tasklist/TasklistModule.tsx:522 | onClick | API_CANDIDATE | claimWorkItem |
| UI-1291 | components/tasklist/TasklistModule.tsx:551 | input | LOCAL_UI |  |
| UI-1292 | components/tasklist/TasklistModule.tsx:555 | onChange | LOCAL_DRAFT |  |
| UI-1293 | components/tasklist/TasklistModule.tsx:566 | select | LOCAL_UI |  |
| UI-1294 | components/tasklist/TasklistModule.tsx:568 | onChange | LOCAL_DRAFT |  |
| UI-1295 | components/tasklist/TasklistModule.tsx:582 | select | LOCAL_UI |  |
| UI-1296 | components/tasklist/TasklistModule.tsx:584 | onChange | LOCAL_DRAFT |  |
| UI-1297 | components/tasklist/TasklistModule.tsx:599 | input | LOCAL_DRAFT |  |
| UI-1298 | components/tasklist/TasklistModule.tsx:602 | onChange | LOCAL_DRAFT |  |
| UI-1299 | components/tasklist/TasklistModule.tsx:621 | onClick | LOCAL_UI |  |
| UI-1300 | components/tasklist/TasklistModule.tsx:629 | onClick | LOCAL_UI |  |
| UI-1301 | components/tasklist/TasklistModule.tsx:630 | input | LOCAL_DRAFT |  |
| UI-1302 | components/tasklist/TasklistModule.tsx:633 | onChange | LOCAL_DRAFT |  |
| UI-1303 | components/tasklist/TasklistModule.tsx:697 | button | API_CANDIDATE | unclaimWorkItem |
| UI-1304 | components/tasklist/TasklistModule.tsx:698 | onClick | API_CANDIDATE | unclaimWorkItem |
| UI-1305 | components/tasklist/TasklistModule.tsx:705 | button | API_CANDIDATE | claimWorkItem |
| UI-1306 | components/tasklist/TasklistModule.tsx:706 | onClick | API_CANDIDATE | claimWorkItem |
| UI-1307 | components/tasklist/TasklistModule.tsx:758 | textarea | LOCAL_DRAFT |  |
| UI-1308 | components/tasklist/TasklistModule.tsx:761 | onChange | LOCAL_DRAFT |  |
| UI-1309 | components/tasklist/TasklistModule.tsx:765 | select | LOCAL_DRAFT |  |
| UI-1310 | components/tasklist/TasklistModule.tsx:767 | onChange | LOCAL_DRAFT |  |
| UI-1311 | components/tasklist/TasklistModule.tsx:776 | input | LOCAL_DRAFT |  |
| UI-1312 | components/tasklist/TasklistModule.tsx:779 | onChange | LOCAL_DRAFT |  |
| UI-1313 | components/tasklist/TasklistModule.tsx:785 | input | LOCAL_DRAFT |  |
| UI-1314 | components/tasklist/TasklistModule.tsx:788 | onChange | LOCAL_DRAFT |  |
| UI-1315 | components/tasklist/TasklistModule.tsx:804 | button | API_CANDIDATE | completeWorkItem;targetSubmitWorkItemForm |
| UI-1316 | components/tasklist/TasklistModule.tsx:805 | onClick | API_CANDIDATE | completeWorkItem;targetSubmitWorkItemForm |
| UI-1317 | components/tasklist/TasklistModule.tsx:811 | button | API_CANDIDATE | completeWorkItem;targetSubmitWorkItemForm |
| UI-1318 | components/tasklist/TasklistModule.tsx:812 | onClick | API_CANDIDATE | completeWorkItem;targetSubmitWorkItemForm |
| UI-1319 | components/tasklist/TasklistModule.tsx:818 | button | API_CANDIDATE | completeWorkItem;targetSubmitWorkItemForm |
| UI-1320 | components/tasklist/TasklistModule.tsx:819 | onClick | API_CANDIDATE | completeWorkItem;targetSubmitWorkItemForm |
| UI-1321 | components/tasklist/TasklistModule.tsx:857 | button | API_CANDIDATE | targetBatchWorkItemActions |
| UI-1322 | components/tasklist/TasklistModule.tsx:858 | onClick | API_CANDIDATE | targetBatchWorkItemActions |
| UI-1323 | components/tasklist/TasklistModule.tsx:865 | button | API_CANDIDATE | targetBatchWorkItemActions |
| UI-1324 | components/tasklist/TasklistModule.tsx:866 | onClick | API_CANDIDATE | targetBatchWorkItemActions |
| UI-1325 | components/tasklist/TasklistModule.tsx:873 | button | LOCAL_UI |  |
| UI-1326 | components/tasklist/TasklistModule.tsx:874 | onClick | LOCAL_UI |  |

## Review rule

REVIEW_REQUIRED blocks parity completion. Native controls without a direct handler can be form submitters, wrapped/delegated controls, disabled placeholders or unimplemented UI. Inspect the nearest form/component and handler body. Inline mutations and dynamic labels require manual mapping; do not add a guessed endpoint to inflate coverage.
LOCAL_UI/LOCAL_DRAFT are lexical candidates too: changing a permission toggle might require immediate owner mutation while another toggle is staged until Save. Verify persistence boundary. Demo persona/sample conflicts stay development-only. Permanent delete requires Governance/retention design, not ordinary entity DELETE.
