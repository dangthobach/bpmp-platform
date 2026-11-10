# Prototype control/event inventory

Generated lexical evidence. Includes native controls, non-button events, and custom callback props.
IDs are snapshot-local; regeneration can renumber them. File hashes are in JSON.
A control and its event are separate rows, not separate user journeys.
Expressions are evidence, not proof that an action persists or succeeds.
Conditional/dynamic labels, forwarded props, keyboard behavior and handler semantics require manual/browser review.
No browser walkthrough was performed. This is not a certified exhaustive runtime interaction inventory.

| File | Native controls | Event props |
|---|---:|---:|
| App.tsx | 0 | 20 |
| components/common/Header.tsx | 9 | 8 |
| components/common/Sidebar.tsx | 3 | 3 |
| components/foundation/EndpointsModal.tsx | 16 | 13 |
| components/foundation/FoundationModule.tsx | 15 | 15 |
| components/identity/IdentityModule.tsx | 167 | 178 |
| components/modeler/DmnTableModule.tsx | 35 | 48 |
| components/modeler/DmnVersionCompareModal.tsx | 5 | 5 |
| components/modeler/DmnVersionHistorySidebar.tsx | 14 | 13 |
| components/modeler/DmnVersionInspectModal.tsx | 3 | 3 |
| components/modeler/DmnVisualLogicEditor.tsx | 19 | 23 |
| components/modeler/ModelerModule.tsx | 23 | 37 |
| components/modeler/ModelerPalette.tsx | 4 | 7 |
| components/modeler/ModelerPropertiesPanel.tsx | 45 | 36 |
| components/operation/OperationModule.tsx | 30 | 35 |
| components/optimize/OptimizeModule.tsx | 12 | 20 |
| components/optimize/ProcessHealthReport.tsx | 9 | 9 |
| components/optimize/ProcessThroughputDashboard.tsx | 11 | 11 |
| components/portal/ClientPortalModule.tsx | 95 | 114 |
| components/portal/EmbeddedProcessViewer.tsx | 22 | 23 |
| components/portal/PortalMetricsDashboard.tsx | 4 | 6 |
| components/task-management/AccessAuditLog.tsx | 10 | 10 |
| components/task-management/TaskManagementModule.tsx | 33 | 38 |
| components/tasklist/TasklistModule.tsx | 31 | 36 |
| main.tsx | 0 | 0 |

## App.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0001 | 123 | event-prop | onSelectModule | {(mod) => { if (mod === 'client-portal') { setActiveWorkspace('client-portal'); } else { setActiveWorkspace('admin-console'); setCurrentModule(mod); } }} |
| UI-0002 | 132 | event-prop | onToggleCollapse | {() => setSidebarCollapsed(!sidebarCollapsed)} |
| UI-0003 | 134 | event-prop | onOpenEndpointsModal | {() => setShowEndpointsModal(true)} |
| UI-0004 | 136 | event-prop | onSelectWorkspace | {setActiveWorkspace} |
| UI-0005 | 146 | event-prop | onToggleDarkMode | {() => setIsDarkMode(!isDarkMode)} |
| UI-0006 | 148 | event-prop | onSelectTenant | {(t) => { setTenantName(t); showNotification(&#96;Đã chuyển đổi ngữ cảnh sang tenant: ${t}&#96;); }} |
| UI-0007 | 153 | event-prop | onSelectWorkspace | {setActiveWorkspace} |
| UI-0008 | 160 | event-prop | onNotify | {showNotification} |
| UI-0009 | 161 | event-prop | onOpenAdminConsole | {() => setActiveWorkspace('admin-console')} |
| UI-0010 | 163 | event-prop | onProcessInstanceCreated | {(newInstance) => { setInstances(prev => [newInstance, ...prev]); showNotification(&#96;Đã tạo tiến trình BPM Core: ${newInstance.recordCode} (${newInstance.processName})&#96;); }} |
| UI-0011 | 173 | event-prop | onUpdateProcess | {handleUpdateProcess} |
| UI-0012 | 175 | event-prop | onSelectProcess | {setSelectedProcessKey} |
| UI-0013 | 176 | event-prop | onDeployToEngine | {handleDeployToEngine} |
| UI-0014 | 184 | event-prop | onUpdateInstances | {setInstances} |
| UI-0015 | 185 | event-prop | onSelectProcess | {setSelectedProcessKey} |
| UI-0016 | 207 | event-prop | onTaskCompleted | {handleTaskCompleted} |
| UI-0017 | 213 | event-prop | onRegisterCustomWorkflow | {handleRegisterCustomWorkflow} |
| UI-0018 | 214 | event-prop | onOpenEndpointsModal | {() => setShowEndpointsModal(true)} |
| UI-0019 | 215 | event-prop | onNavigateToModeler | {(key) => { setSelectedProcessKey(key); setCurrentModule('modeler'); }} |
| UI-0020 | 229 | event-prop | onClose | {() => setShowEndpointsModal(false)} |

## components/common/Header.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0021 | 123 | event-prop | onClick | {() => crumb.isTenant && setShowTenantMenu(!showTenantMenu)} |
| UI-0022 | 141 | control | button | <button key={t} onClick={() => { onSelectTenant(t); setShowTenantMenu(false); }} className={&#96;w-full text-left px-2.5 py-2 rounded-lg text-xs flex items-center justify-between transition-colors cursor-pointer ${ tenantName === t ? 'bg-em |
| UI-0023 | 143 | event-prop | onClick | {() => { onSelectTenant(t); setShowTenantMenu(false); }} |
| UI-0024 | 169 | control | button | <button onClick={() => onSelectWorkspace('client-portal')} className={&#96;flex items-center gap-1.5 px-2.5 py-1 rounded-md text-[11px] font-bold transition-all cursor-pointer ${ activeWorkspace === 'client-portal' ? 'bg-emerald-600 text-wh |
| UI-0025 | 170 | event-prop | onClick | {() => onSelectWorkspace('client-portal')} |
| UI-0026 | 181 | control | button | <button onClick={() => onSelectWorkspace('admin-console')} className={&#96;flex items-center gap-1.5 px-2.5 py-1 rounded-md text-[11px] font-bold transition-all cursor-pointer ${ activeWorkspace === 'admin-console' ? 'bg-slate-900 text-whit |
| UI-0027 | 182 | event-prop | onClick | {() => onSelectWorkspace('admin-console')} |
| UI-0028 | 199 | control | input | <input type="text" placeholder="Tìm kiếm tác vụ, quy trình..." className="w-52 lg:w-64 pl-8 pr-12 py-1.5 text-xs bg-slate-100/70 dark:bg-slate-800/60 hover:bg-slate-100 dark:hover:bg-slate-800 border border-slate-200/80 dark:border-slate-70 |
| UI-0029 | 211 | control | button | <button onClick={() => setNotificationsOpen(!notificationsOpen)} className="w-8 h-8 rounded-lg border border-slate-200/70 dark:border-slate-800 flex items-center justify-center text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:ho |
| UI-0030 | 212 | event-prop | onClick | {() => setNotificationsOpen(!notificationsOpen)} |
| UI-0031 | 242 | control | button | <button className="h-8 px-2 rounded-lg border border-slate-200/70 dark:border-slate-800 flex items-center gap-1 text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-100 hover:bg-slate-100 dark:hover:bg-slate-800/80  |
| UI-0032 | 251 | control | button | <button onClick={onToggleDarkMode} className="w-8 h-8 rounded-lg border border-slate-200/70 dark:border-slate-800 flex items-center justify-center text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-100 hover:bg-sl |
| UI-0033 | 252 | event-prop | onClick | {onToggleDarkMode} |
| UI-0034 | 263 | control | button | <button onClick={() => setShowUserMenu(!showUserMenu)} className="flex items-center gap-2 p-1 pl-1.5 pr-2.5 rounded-lg hover:bg-slate-100 dark:hover:bg-slate-800 transition-all cursor-pointer border border-transparent hover:border-slate-200 |
| UI-0035 | 264 | event-prop | onClick | {() => setShowUserMenu(!showUserMenu)} |
| UI-0036 | 299 | control | button | <button onClick={() => setShowUserMenu(false)} className="w-full text-left py-1.5 text-rose-600 hover:text-rose-700 dark:text-rose-400 font-medium cursor-pointer" > Đăng xuất tài khoản  |
| UI-0037 | 300 | event-prop | onClick | {() => setShowUserMenu(false)} |

## components/common/Sidebar.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0038 | 83 | control | button | <button key={item.id} onClick={() => onSelectModule(item.id)} className={&#96;w-full flex items-center gap-3 px-3 py-2 rounded-lg text-xs font-medium transition-all group relative cursor-pointer ${ isActive ? 'bg-emerald-50 dark:bg-emerald- |
| UI-0039 | 85 | event-prop | onClick | {() => onSelectModule(item.id)} |
| UI-0040 | 116 | control | button | <button onClick={onOpenEndpointsModal} className="w-full flex items-center gap-2.5 px-2.5 py-1.5 text-xs text-slate-600 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-800 rounded-lg transition-colors cursor-pointer" title="Sourc |
| UI-0041 | 117 | event-prop | onClick | {onOpenEndpointsModal} |
| UI-0042 | 138 | control | button | <button onClick={onToggleCollapse} className="w-6 h-6 flex items-center justify-center text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 hover:bg-slate-200/60 dark:hover:bg-slate-700 rounded transition-colors cursor-pointer" tit |
| UI-0043 | 139 | event-prop | onClick | {onToggleCollapse} |

## components/foundation/EndpointsModal.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0044 | 92 | control | button | <button onClick={onClose} className="text-slate-400 hover:text-slate-600 p-1 rounded"> ✕  |
| UI-0045 | 92 | event-prop | onClick | {onClose} |
| UI-0046 | 100 | control | button | <button onClick={() => { setEditingEp(null); setServiceName(''); setEndpointUrl(''); setShowAddModal(true); }} className="flex items-center gap-1.5 px-3.5 py-2 bg-emerald-700 hover:bg-emerald-800 text-white rounded-md text-xs font-semibold  |
| UI-0047 | 101 | event-prop | onClick | {() => { setEditingEp(null); setServiceName(''); setEndpointUrl(''); setShowAddModal(true); }} |
| UI-0048 | 156 | control | button | <button onClick={() => handleStartEdit(ep)} className="p-1 text-slate-500 hover:text-slate-800 dark:hover:text-slate-200 rounded" title="Edit endpoint" > <Pencil className="w-3.5 h-3.5" />  |
| UI-0049 | 157 | event-prop | onClick | {() => handleStartEdit(ep)} |
| UI-0050 | 163 | control | button | <button onClick={() => handleDelete(ep.id)} className="p-1 text-red-500 hover:text-red-700 rounded" title="Delete endpoint" > <Trash2 className="w-3.5 h-3.5" />  |
| UI-0051 | 164 | event-prop | onClick | {() => handleDelete(ep.id)} |
| UI-0052 | 185 | control | button | <button className="p-1 text-slate-400 hover:text-slate-600 disabled:opacity-40" disabled> <ChevronLeft className="w-4 h-4" />  |
| UI-0053 | 191 | control | button | <button className="p-1 text-slate-400 hover:text-slate-600 disabled:opacity-40" disabled> <ChevronRight className="w-4 h-4" />  |
| UI-0054 | 198 | control | select | <select className="bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs"> <option value="10">10</option> <option value="20">20</option>  |
| UI-0055 | 209 | control | button | <button onClick={onClose} className="text-xs text-emerald-700 dark:text-emerald-400 font-semibold hover:underline" > Close  |
| UI-0056 | 210 | event-prop | onClick | {onClose} |
| UI-0057 | 226 | control | button | <button onClick={() => setShowAddModal(false)} className="text-slate-400 hover:text-slate-600">✕ |
| UI-0058 | 226 | event-prop | onClick | {() => setShowAddModal(false)} |
| UI-0059 | 231 | control | input | <input type="text" value={serviceName} onChange={(e) => setServiceName(e.target.value)} placeholder="e.g. TradeFinance View" className="w-full px-2.5 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded" / |
| UI-0060 | 234 | event-prop | onChange | {(e) => setServiceName(e.target.value)} |
| UI-0061 | 241 | control | select | <select value={serviceType} onChange={(e) => setServiceType(e.target.value)} className="w-full px-2.5 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded font-semibold" > <option value="Task View">Task Vi |
| UI-0062 | 243 | event-prop | onChange | {(e) => setServiceType(e.target.value)} |
| UI-0063 | 253 | control | input | <input type="text" value={endpointUrl} onChange={(e) => setEndpointUrl(e.target.value)} placeholder="https://ecm.aws.ecmbpm.vn/api/..." className="w-full px-2.5 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 |
| UI-0064 | 256 | event-prop | onChange | {(e) => setEndpointUrl(e.target.value)} |
| UI-0065 | 264 | control | select | <select value={httpMethod} onChange={(e) => setHttpMethod(e.target.value as any)} className="w-full px-2.5 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded font-semibold" > <option value="GET">GET</opt |
| UI-0066 | 266 | event-prop | onChange | {(e) => setHttpMethod(e.target.value as any)} |
| UI-0067 | 277 | control | select | <select value={authType} onChange={(e) => setAuthType(e.target.value as any)} className="w-full px-2.5 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded font-semibold" > <option value="NONE">NONE</optio |
| UI-0068 | 279 | event-prop | onChange | {(e) => setAuthType(e.target.value as any)} |
| UI-0069 | 291 | control | button | <button onClick={() => setShowAddModal(false)} className="px-3 py-1.5 border border-slate-300 rounded text-xs font-semibold"> Hủy  |
| UI-0070 | 291 | event-prop | onClick | {() => setShowAddModal(false)} |
| UI-0071 | 294 | control | button | <button onClick={handleSaveEndpoint} className="px-4 py-1.5 bg-emerald-700 text-white rounded text-xs font-semibold hover:bg-emerald-800"> Lưu Endpoint  |
| UI-0072 | 294 | event-prop | onClick | {handleSaveEndpoint} |

## components/foundation/FoundationModule.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0073 | 145 | control | button | <button onClick={() => setActiveTab('template-generator')} className={&#96;py-3 transition-colors relative cursor-pointer flex items-center gap-1.5 ${ activeTab === 'template-generator' ? 'text-emerald-700 dark:text-emerald-400 font-bold' : |
| UI-0074 | 146 | event-prop | onClick | {() => setActiveTab('template-generator')} |
| UI-0075 | 156 | control | button | <button onClick={() => setActiveTab('allocation')} className={&#96;py-3 transition-colors relative cursor-pointer flex items-center gap-1.5 ${ activeTab === 'allocation' ? 'text-emerald-700 dark:text-emerald-400 font-bold' : 'text-slate-600 |
| UI-0076 | 157 | event-prop | onClick | {() => setActiveTab('allocation')} |
| UI-0077 | 167 | control | button | <button onClick={() => setActiveTab('notifications')} className={&#96;py-3 transition-colors relative cursor-pointer flex items-center gap-1.5 ${ activeTab === 'notifications' ? 'text-emerald-700 dark:text-emerald-400 font-bold' : 'text-sla |
| UI-0078 | 168 | event-prop | onClick | {() => setActiveTab('notifications')} |
| UI-0079 | 178 | control | button | <button onClick={() => setActiveTab('documents')} className={&#96;py-3 transition-colors relative cursor-pointer flex items-center gap-1.5 ${ activeTab === 'documents' ? 'text-emerald-700 dark:text-emerald-400 font-bold' : 'text-slate-600 d |
| UI-0080 | 179 | event-prop | onClick | {() => setActiveTab('documents')} |
| UI-0081 | 190 | control | button | <button onClick={onOpenEndpointsModal} className="flex items-center gap-1.5 px-3 py-1.5 border border-slate-300 dark:border-slate-700 hover:bg-slate-100 text-slate-700 dark:text-slate-300 rounded text-xs font-semibold cursor-pointer" > <Rad |
| UI-0082 | 191 | event-prop | onClick | {onOpenEndpointsModal} |
| UI-0083 | 209 | control | button | <button onClick={() => onNavigateToModeler(workflowKey)} className="px-3 py-1.5 bg-emerald-700 text-white rounded text-xs font-bold hover:bg-emerald-800" > Mở trong Modeler →  |
| UI-0084 | 210 | event-prop | onClick | {() => onNavigateToModeler(workflowKey)} |
| UI-0085 | 236 | control | button | <button key={domain} type="button" onClick={() => handleSelectDomainPreset(domain)} className={&#96;p-3 rounded-lg border text-left text-xs transition-all cursor-pointer ${ domainType === domain ? 'border-emerald-600 bg-emerald-50/80 dark:b |
| UI-0086 | 239 | event-prop | onClick | {() => handleSelectDomainPreset(domain)} |
| UI-0087 | 257 | control | input | <input type="text" value={workflowKey} onChange={(e) => setWorkflowKey(e.target.value)} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded font-mono font-bold" /> </div> <div> <la |
| UI-0088 | 260 | event-prop | onChange | {(e) => setWorkflowKey(e.target.value)} |
| UI-0089 | 267 | control | select | <select value={targetEngine} onChange={(e) => setTargetEngine(e.target.value as any)} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded font-semibold" > <option value="Camunda">C |
| UI-0090 | 269 | event-prop | onChange | {(e) => setTargetEngine(e.target.value as any)} |
| UI-0091 | 279 | control | input | <input type="text" value={workflowName} onChange={(e) => setWorkflowName(e.target.value)} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded font-semibold" /> </div> </div> {/* Dy |
| UI-0092 | 282 | event-prop | onChange | {(e) => setWorkflowName(e.target.value)} |
| UI-0093 | 294 | control | button | <button type="button" onClick={() => setCustomSteps([...customSteps, &#96;BƯỚC BỔ SUNG ${customSteps.length + 1}&#96;])} className="text-xs text-emerald-700 dark:text-emerald-400 font-semibold hover:underline flex items-center gap-1 cursor- |
| UI-0094 | 296 | event-prop | onClick | {() => setCustomSteps([...customSteps, &#96;BƯỚC BỔ SUNG ${customSteps.length + 1}&#96;])} |
| UI-0095 | 310 | control | input | <input type="text" value={step} onChange={(e) => { const updated = [...customSteps]; updated[idx] = e.target.value; setCustomSteps(updated); }} className="flex-1 px-3 py-1.5 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border- |
| UI-0096 | 313 | event-prop | onChange | {(e) => { const updated = [...customSteps]; updated[idx] = e.target.value; setCustomSteps(updated); }} |
| UI-0097 | 321 | control | button | <button type="button" onClick={() => setCustomSteps(customSteps.filter((_, i) => i !== idx))} className="p-1.5 text-slate-400 hover:text-red-500 rounded" > <Trash2 className="w-3.5 h-3.5" />  |
| UI-0098 | 323 | event-prop | onClick | {() => setCustomSteps(customSteps.filter((_, i) => i !== idx))} |
| UI-0099 | 336 | control | button | <button type="button" onClick={handleCreateCustomWorkflow} className="px-5 py-2.5 bg-emerald-700 hover:bg-emerald-800 text-white rounded-lg text-xs font-bold shadow-sm transition-all flex items-center gap-2 cursor-pointer" > <Wand2 classNam |
| UI-0100 | 338 | event-prop | onClick | {handleCreateCustomWorkflow} |
| UI-0101 | 378 | control | button | <button onClick={() => { setAllocationRules(allocationRules.map(r => r.id === rule.id ? { ...r, active: !r.active } : r)); }} className={&#96;px-3 py-1 rounded text-xs font-semibold ${ rule.active ? 'bg-emerald-50 text-emerald-700 dark:bg-e |
| UI-0102 | 379 | event-prop | onClick | {() => { setAllocationRules(allocationRules.map(r => r.id === rule.id ? { ...r, active: !r.active } : r)); }} |

## components/identity/IdentityModule.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0103 | 809 | control | button | <button onClick={() => setSubView('dashboard')} className="hover:text-emerald-700 dark:hover:text-emerald-400 cursor-pointer transition-colors" > Dashboard  |
| UI-0104 | 810 | event-prop | onClick | {() => setSubView('dashboard')} |
| UI-0105 | 818 | control | button | <button onClick={() => setSubView('dashboard')} className="hover:text-emerald-700 dark:hover:text-emerald-400 cursor-pointer font-semibold transition-colors" > Identity  |
| UI-0106 | 819 | event-prop | onClick | {() => setSubView('dashboard')} |
| UI-0107 | 849 | control | button | <button onClick={() => setShowAddUserModal(true)} className="flex items-center gap-1.5 px-3.5 py-1.5 bg-emerald-600 hover:bg-emerald-500 active:scale-[0.98] text-white rounded-lg text-xs font-semibold shadow-xs transition-all cursor-pointer |
| UI-0108 | 850 | event-prop | onClick | {() => setShowAddUserModal(true)} |
| UI-0109 | 861 | control | button | <button onClick={() => setSubView('dashboard')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer flex items-center gap-1.5 whitespace-nowrap ${ subView === 'dashboard' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:te |
| UI-0110 | 862 | event-prop | onClick | {() => setSubView('dashboard')} |
| UI-0111 | 873 | control | button | <button onClick={() => setSubView('users')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer flex items-center gap-1.5 whitespace-nowrap ${ subView === 'users' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-white |
| UI-0112 | 874 | event-prop | onClick | {() => setSubView('users')} |
| UI-0113 | 888 | control | button | <button onClick={() => setSubView('roles')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer flex items-center gap-1.5 whitespace-nowrap ${ subView === 'roles' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-white |
| UI-0114 | 889 | event-prop | onClick | {() => setSubView('roles')} |
| UI-0115 | 903 | control | button | <button onClick={() => setSubView('permissions')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer flex items-center gap-1.5 whitespace-nowrap ${ subView === 'permissions' ? 'bg-white dark:bg-slate-900 text-slate-900 dar |
| UI-0116 | 904 | event-prop | onClick | {() => setSubView('permissions')} |
| UI-0117 | 918 | control | button | <button onClick={() => setSubView('resources')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer flex items-center gap-1.5 whitespace-nowrap ${ subView === 'resources' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:te |
| UI-0118 | 919 | event-prop | onClick | {() => setSubView('resources')} |
| UI-0119 | 933 | control | button | <button onClick={() => setSubView('audit')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer flex items-center gap-1.5 whitespace-nowrap ${ subView === 'audit' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-white |
| UI-0120 | 934 | event-prop | onClick | {() => setSubView('audit')} |
| UI-0121 | 958 | event-prop | onClick | {() => setSubView('users')} |
| UI-0122 | 971 | event-prop | onClick | {() => { setStatusFilter('ACTIVE'); setSubView('users'); }} |
| UI-0123 | 984 | event-prop | onClick | {() => { setStatusFilter('LOCKED'); setSubView('users'); }} |
| UI-0124 | 997 | event-prop | onClick | {() => setSubView('roles')} |
| UI-0125 | 1013 | event-prop | onClick | {() => setSubView('permissions')} |
| UI-0126 | 1026 | event-prop | onClick | {() => setSubView('resources')} |
| UI-0127 | 1039 | event-prop | onClick | {() => setSubView('resources')} |
| UI-0128 | 1060 | control | button | <button onClick={() => setSubView('users')} className="flex items-center gap-2 px-4 py-2 bg-emerald-600 hover:bg-emerald-700 text-white rounded-lg text-xs font-semibold transition-colors cursor-pointer shadow-xs" > <Users className="w-3.5 h |
| UI-0129 | 1061 | event-prop | onClick | {() => setSubView('users')} |
| UI-0130 | 1067 | control | button | <button onClick={() => setSubView('roles')} className="flex items-center gap-2 px-4 py-2 bg-emerald-600 hover:bg-emerald-700 text-white rounded-lg text-xs font-semibold transition-colors cursor-pointer shadow-xs" > <Shield className="w-3.5  |
| UI-0131 | 1068 | event-prop | onClick | {() => setSubView('roles')} |
| UI-0132 | 1074 | control | button | <button onClick={() => setSubView('permissions')} className="flex items-center gap-2 px-4 py-2 bg-emerald-600 hover:bg-emerald-700 text-white rounded-lg text-xs font-semibold transition-colors cursor-pointer shadow-xs" > <KeyRound className |
| UI-0133 | 1075 | event-prop | onClick | {() => setSubView('permissions')} |
| UI-0134 | 1081 | control | button | <button onClick={() => setSubView('audit')} className="flex items-center gap-2 px-4 py-2 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-700 hover:bg-slate-100 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-300  |
| UI-0135 | 1082 | event-prop | onClick | {() => setSubView('audit')} |
| UI-0136 | 1135 | control | button | <button onClick={() => handleBulkExportCsv()} className="flex items-center gap-1.5 px-3 py-1.5 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-200 rounded-lg text-xs font-semibold sha |
| UI-0137 | 1136 | event-prop | onClick | {() => handleBulkExportCsv()} |
| UI-0138 | 1144 | control | button | <button onClick={() => setShowAddUserModal(true)} className="flex items-center gap-1.5 px-3.5 py-1.5 bg-emerald-700 hover:bg-emerald-800 text-white rounded-lg text-xs font-semibold shadow-xs cursor-pointer" > <Plus className="w-3.5 h-3.5" / |
| UI-0139 | 1145 | event-prop | onClick | {() => setShowAddUserModal(true)} |
| UI-0140 | 1173 | control | button | <button onClick={() => setBulkActionModal('roles')} className="flex items-center gap-1 px-2.5 py-1 bg-white dark:bg-slate-900 text-slate-800 dark:text-slate-100 hover:bg-slate-100 dark:hover:bg-slate-800 border border-slate-300 dark:border- |
| UI-0141 | 1174 | event-prop | onClick | {() => setBulkActionModal('roles')} |
| UI-0142 | 1182 | control | button | <button onClick={() => setBulkActionModal('permissions')} className="flex items-center gap-1 px-2.5 py-1 bg-emerald-700 hover:bg-emerald-800 text-white rounded-lg text-xs font-semibold transition-all cursor-pointer shadow-2xs" title="Gán tr |
| UI-0143 | 1183 | event-prop | onClick | {() => setBulkActionModal('permissions')} |
| UI-0144 | 1191 | control | button | <button onClick={() => setBulkActionModal('delete')} className="flex items-center gap-1 px-2.5 py-1 bg-rose-50 dark:bg-rose-950/40 text-rose-700 dark:text-rose-400 hover:bg-rose-100 dark:hover:bg-rose-900/60 border border-rose-300 dark:bord |
| UI-0145 | 1192 | event-prop | onClick | {() => setBulkActionModal('delete')} |
| UI-0146 | 1200 | control | button | <button onClick={() => handleBulkLock('LOCKED')} className="flex items-center gap-1 px-2.5 py-1 bg-white dark:bg-slate-900 text-rose-700 dark:text-rose-400 hover:bg-rose-50 dark:hover:bg-rose-950/40 border border-rose-200 dark:border-rose-9 |
| UI-0147 | 1201 | event-prop | onClick | {() => handleBulkLock('LOCKED')} |
| UI-0148 | 1208 | control | button | <button onClick={() => handleBulkLock('ACTIVE')} className="flex items-center gap-1 px-2.5 py-1 bg-white dark:bg-slate-900 text-emerald-700 dark:text-emerald-400 hover:bg-emerald-50 dark:hover:bg-emerald-950/40 border border-emerald-200 dar |
| UI-0149 | 1209 | event-prop | onClick | {() => handleBulkLock('ACTIVE')} |
| UI-0150 | 1216 | control | button | <button onClick={() => setBulkActionModal('department')} className="flex items-center gap-1 px-2.5 py-1 bg-white dark:bg-slate-900 text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-slate-800 border border-slate-300 dark:bo |
| UI-0151 | 1217 | event-prop | onClick | {() => setBulkActionModal('department')} |
| UI-0152 | 1224 | control | button | <button onClick={() => setBulkActionModal('security')} className="flex items-center gap-1 px-2.5 py-1 bg-white dark:bg-slate-900 text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-slate-800 border border-slate-300 dark:bord |
| UI-0153 | 1225 | event-prop | onClick | {() => setBulkActionModal('security')} |
| UI-0154 | 1232 | control | button | <button onClick={handleClearSelection} className="p-1 text-slate-500 hover:text-slate-800 dark:hover:text-slate-200 rounded-md hover:bg-slate-200/60 dark:hover:bg-slate-800 transition-colors ml-1 cursor-pointer" title="Bỏ chọn tất cả" > <X  |
| UI-0155 | 1233 | event-prop | onClick | {handleClearSelection} |
| UI-0156 | 1247 | control | input | <input type="text" placeholder="Tìm theo tên, username, email, phòng ban..." value={searchTerm} onChange={(e) => setSearchTerm(e.target.value)} className="w-full pl-8 pr-8 py-1.5 text-xs bg-slate-50 dark:bg-slate-800/60 border border-slate- |
| UI-0157 | 1251 | event-prop | onChange | {(e) => setSearchTerm(e.target.value)} |
| UI-0158 | 1256 | control | button | <button onClick={() => setSearchTerm('')} className="absolute right-2.5 top-2 text-slate-400 hover:text-slate-600 text-xs cursor-pointer" > ✕  |
| UI-0159 | 1257 | event-prop | onClick | {() => setSearchTerm('')} |
| UI-0160 | 1270 | control | button | <button onClick={() => setStatusFilter('ALL')} className={&#96;px-3 py-1 rounded-lg text-xs font-semibold cursor-pointer transition-all ${ statusFilter === 'ALL' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 shadow-xs' :  |
| UI-0161 | 1271 | event-prop | onClick | {() => setStatusFilter('ALL')} |
| UI-0162 | 1278 | control | button | <button onClick={() => setStatusFilter('ACTIVE')} className={&#96;px-3 py-1 rounded-lg text-xs font-semibold cursor-pointer transition-all ${ statusFilter === 'ACTIVE' ? 'bg-white dark:bg-slate-900 text-emerald-600 dark:text-emerald-400 sha |
| UI-0163 | 1279 | event-prop | onClick | {() => setStatusFilter('ACTIVE')} |
| UI-0164 | 1286 | control | button | <button onClick={() => setStatusFilter('LOCKED')} className={&#96;px-3 py-1 rounded-lg text-xs font-semibold cursor-pointer transition-all ${ statusFilter === 'LOCKED' ? 'bg-white dark:bg-slate-900 text-rose-600 dark:text-rose-400 shadow-xs |
| UI-0165 | 1287 | event-prop | onClick | {() => setStatusFilter('LOCKED')} |
| UI-0166 | 1307 | control | button | <button type="button" onClick={handleToggleSelectAll} className="text-slate-600 dark:text-slate-300 hover:text-emerald-600 cursor-pointer" title={ filteredUsers.length > 0 && filteredUsers.every(u => selectedUserIds.includes(u.id)) ? 'Bỏ ch |
| UI-0167 | 1309 | event-prop | onClick | {handleToggleSelectAll} |
| UI-0168 | 1327 | control | button | <button type="button" onClick={() => setIsSelectionDropdownOpen(!isSelectionDropdownOpen)} className="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer p-0.5 rounded" title="Tùy chọn chọn lô nhanh" > <ChevronDown  |
| UI-0169 | 1329 | event-prop | onClick | {() => setIsSelectionDropdownOpen(!isSelectionDropdownOpen)} |
| UI-0170 | 1344 | control | button | <button type="button" onClick={() => { const allIds = filteredUsers.map(u => u.id); setSelectedUserIds(allIds); setIsSelectionDropdownOpen(false); showToast(&#96;Đã chọn toàn bộ ${allIds.length} người dùng đang hiển thị.&#96;); }} className |
| UI-0171 | 1346 | event-prop | onClick | {() => { const allIds = filteredUsers.map(u => u.id); setSelectedUserIds(allIds); setIsSelectionDropdownOpen(false); showToast(&#96;Đã chọn toàn bộ ${allIds.length} người dùng đang hiển thị.&#96;); }} |
| UI-0172 | 1358 | control | button | <button type="button" onClick={handleSelectActiveOnly} className="w-full px-3 py-1.5 text-left hover:bg-slate-100 dark:hover:bg-slate-800 flex items-center justify-between text-slate-700 dark:text-slate-200 cursor-pointer" > <span>Chỉ chọn  |
| UI-0173 | 1360 | event-prop | onClick | {handleSelectActiveOnly} |
| UI-0174 | 1367 | control | button | <button type="button" onClick={handleSelectLockedOnly} className="w-full px-3 py-1.5 text-left hover:bg-slate-100 dark:hover:bg-slate-800 flex items-center justify-between text-slate-700 dark:text-slate-200 cursor-pointer" > <span>Chỉ chọn  |
| UI-0175 | 1369 | event-prop | onClick | {handleSelectLockedOnly} |
| UI-0176 | 1378 | control | button | <button type="button" onClick={handleInvertSelection} className="w-full px-3 py-1.5 text-left hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-700 dark:text-slate-200 cursor-pointer" > Đảo ngược lựa chọn  |
| UI-0177 | 1380 | event-prop | onClick | {handleInvertSelection} |
| UI-0178 | 1385 | control | button | <button type="button" onClick={handleClearSelection} className="w-full px-3 py-1.5 text-left hover:bg-rose-50 dark:hover:bg-rose-950/30 text-rose-600 cursor-pointer" > Bỏ chọn tất cả  |
| UI-0179 | 1387 | event-prop | onClick | {handleClearSelection} |
| UI-0180 | 1426 | control | button | <button type="button" onClick={() => handleToggleUser(user.id)} className="text-slate-500 hover:text-emerald-600 cursor-pointer" > {isSelected ? ( <CheckSquare className="w-4 h-4 text-emerald-600" /> ) : ( <Square clas |
| UI-0181 | 1428 | event-prop | onClick | {() => handleToggleUser(user.id)} |
| UI-0182 | 1508 | control | button | <button onClick={() => toggleUserLock(user.id)} className={&#96;p-1.5 rounded-lg border transition-all cursor-pointer ${ user.status === 'ACTIVE' ? 'text-rose-600 border-rose-200 hover:bg-rose-50 dark:border-rose-900 dark:hover:bg-rose-950/ |
| UI-0183 | 1509 | event-prop | onClick | {() => toggleUserLock(user.id)} |
| UI-0184 | 1552 | control | button | <button onClick={handleBulkExportRoles} className="flex items-center gap-1.5 px-3 py-1.5 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-200 rounded-lg text-xs font-semibold shadow-2x |
| UI-0185 | 1553 | event-prop | onClick | {handleBulkExportRoles} |
| UI-0186 | 1582 | control | button | <button onClick={() => { setBulkRoleTargetUsers([]); setBulkRoleAssignMode('add'); setBulkRoleActionModal('assignToUsers'); }} className="flex items-center gap-1 px-2.5 py-1 bg-amber-700 hover:bg-amber-800 text-white rounded-lg text-xs font |
| UI-0187 | 1583 | event-prop | onClick | {() => { setBulkRoleTargetUsers([]); setBulkRoleAssignMode('add'); setBulkRoleActionModal('assignToUsers'); }} |
| UI-0188 | 1596 | control | button | <button onClick={() => setBulkRoleActionModal('grantPerms')} className="flex items-center gap-1 px-2.5 py-1 bg-white dark:bg-slate-900 text-amber-900 dark:text-amber-200 hover:bg-amber-100/50 dark:hover:bg-amber-950/40 border border-amber-3 |
| UI-0189 | 1597 | event-prop | onClick | {() => setBulkRoleActionModal('grantPerms')} |
| UI-0190 | 1606 | control | button | <button onClick={() => setBulkRoleActionModal('changeType')} className="flex items-center gap-1 px-2.5 py-1 bg-white dark:bg-slate-900 text-slate-800 dark:text-slate-100 hover:bg-slate-100 dark:hover:bg-slate-800 border border-slate-300 dar |
| UI-0191 | 1607 | event-prop | onClick | {() => setBulkRoleActionModal('changeType')} |
| UI-0192 | 1616 | control | button | <button onClick={handleBulkExportRoles} className="flex items-center gap-1 px-2.5 py-1 bg-white dark:bg-slate-900 text-slate-800 dark:text-slate-100 hover:bg-slate-100 dark:hover:bg-slate-800 border border-slate-300 dark:border-slate-700 ro |
| UI-0193 | 1617 | event-prop | onClick | {handleBulkExportRoles} |
| UI-0194 | 1626 | control | button | <button onClick={() => setBulkRoleActionModal('delete')} className="flex items-center gap-1 px-2.5 py-1 bg-rose-50 dark:bg-rose-950/40 text-rose-700 dark:text-rose-400 hover:bg-rose-100 dark:hover:bg-rose-900/60 border border-rose-300 dark: |
| UI-0195 | 1627 | event-prop | onClick | {() => setBulkRoleActionModal('delete')} |
| UI-0196 | 1636 | control | button | <button onClick={() => setSelectedRoleNames([])} className="p-1 text-slate-500 hover:text-slate-800 dark:hover:text-slate-200 rounded-md hover:bg-slate-200/60 dark:hover:bg-slate-800 transition-colors ml-1 cursor-pointer" title="Bỏ chọn tất |
| UI-0197 | 1637 | event-prop | onClick | {() => setSelectedRoleNames([])} |
| UI-0198 | 1650 | control | button | <button type="button" onClick={handleToggleSelectAllRoles} className="flex items-center gap-1.5 px-2.5 py-1 rounded-lg border border-slate-200 dark:border-slate-700 text-xs font-semibold text-slate-700 dark:text-slate-300 hover:bg-slate-100 |
| UI-0199 | 1652 | event-prop | onClick | {handleToggleSelectAllRoles} |
| UI-0200 | 1667 | control | input | <input type="text" placeholder="Tìm vai trò theo tên, mô tả, phân nhóm..." value={roleSearchTerm} onChange={(e) => setRoleSearchTerm(e.target.value)} className="w-full pl-8 pr-3 py-1.5 text-xs bg-slate-50 dark:bg-slate-800/60 border border- |
| UI-0201 | 1671 | event-prop | onChange | {(e) => setRoleSearchTerm(e.target.value)} |
| UI-0202 | 1681 | control | button | <button type="button" onClick={() => setRoleViewMode('table')} className={&#96;flex items-center gap-1 px-2.5 py-1 rounded-md text-xs font-semibold transition-all cursor-pointer ${ roleViewMode === 'table' ? 'bg-white dark:bg-slate-700 text |
| UI-0203 | 1683 | event-prop | onClick | {() => setRoleViewMode('table')} |
| UI-0204 | 1694 | control | button | <button type="button" onClick={() => setRoleViewMode('grid')} className={&#96;flex items-center gap-1 px-2.5 py-1 rounded-md text-xs font-semibold transition-all cursor-pointer ${ roleViewMode === 'grid' ? 'bg-white dark:bg-slate-700 text-a |
| UI-0205 | 1696 | event-prop | onClick | {() => setRoleViewMode('grid')} |
| UI-0206 | 1712 | control | button | <button key={type} onClick={() => setRoleTypeFilter(type)} className={&#96;px-2.5 py-1 rounded-lg text-xs font-semibold cursor-pointer transition-all ${ roleTypeFilter === type ? 'bg-amber-600 text-white shadow-xs' : 'bg-slate-100 dark:bg-s |
| UI-0207 | 1714 | event-prop | onClick | {() => setRoleTypeFilter(type)} |
| UI-0208 | 1736 | control | button | <button type="button" onClick={handleToggleSelectAllRoles} className="text-slate-600 dark:text-slate-300 hover:text-amber-600 cursor-pointer" title="Chọn tất cả vai trò đang hiển thị" > {filteredRoles.length > 0 && filteredRoles.every(r =>  |
| UI-0209 | 1738 | event-prop | onClick | {handleToggleSelectAllRoles} |
| UI-0210 | 1777 | event-prop | onClick | {() => handleToggleRole(role.name)} |
| UI-0211 | 1782 | event-prop | onClick | {(e) => e.stopPropagation()} |
| UI-0212 | 1784 | control | button | <button type="button" onClick={() => handleToggleRole(role.name)} className="text-slate-400 hover:text-amber-600 cursor-pointer" > {isSelected ? ( <CheckSquare className="w-4 h-4 text-amber-600" /> ) : ( <Squar |
| UI-0213 | 1786 | event-prop | onClick | {() => handleToggleRole(role.name)} |
| UI-0214 | 1821 | control | button | <button type="button" onClick={(e) => { e.stopPropagation(); setSelectedRoleNames([role.name]); setBulkRoleTargetUsers([]); setBulkRoleAssignMode('add'); setBulkRoleActionModal('assignToUsers'); }} className="px-2 py-0.5 rounded-full font-m |
| UI-0215 | 1823 | event-prop | onClick | {(e) => { e.stopPropagation(); setSelectedRoleNames([role.name]); setBulkRoleTargetUsers([]); setBulkRoleAssignMode('add'); setBulkRoleActionModal('assignToUsers'); }} |
| UI-0216 | 1853 | control | button | <button type="button" onClick={(e) => { e.stopPropagation(); setSelectedRoleNames([role.name]); setBulkRoleActionModal('grantPerms'); }} className="text-[10px] text-amber-700 dark:text-amber-400 hover:underline font-semibold ml-1 cursor-poi |
| UI-0217 | 1855 | event-prop | onClick | {(e) => { e.stopPropagation(); setSelectedRoleNames([role.name]); setBulkRoleActionModal('grantPerms'); }} |
| UI-0218 | 1869 | event-prop | onClick | {(e) => e.stopPropagation()} |
| UI-0219 | 1872 | control | button | <button type="button" onClick={() => { setSelectedRoleNames([role.name]); setBulkRoleTargetUsers([]); setBulkRoleAssignMode('add'); setBulkRoleActionModal('assignToUsers'); }} className="p-1.5 text-slate-500 hover:text-amber-700 hover:bg-am |
| UI-0220 | 1874 | event-prop | onClick | {() => { setSelectedRoleNames([role.name]); setBulkRoleTargetUsers([]); setBulkRoleAssignMode('add'); setBulkRoleActionModal('assignToUsers'); }} |
| UI-0221 | 1885 | control | button | <button type="button" onClick={() => { setSelectedRoleNames([role.name]); setBulkRoleActionModal('grantPerms'); }} className="p-1.5 text-slate-500 hover:text-amber-700 hover:bg-amber-50 dark:hover:bg-amber-950/40 rounded-lg transition-color |
| UI-0222 | 1887 | event-prop | onClick | {() => { setSelectedRoleNames([role.name]); setBulkRoleActionModal('grantPerms'); }} |
| UI-0223 | 1897 | control | button | <button type="button" onClick={() => { setSelectedRoleNames([role.name]); setBulkRoleActionModal('delete'); }} className="p-1.5 text-slate-400 hover:text-rose-600 hover:bg-rose-50 dark:hover:bg-rose-950/40 rounded-lg transition-colors curso |
| UI-0224 | 1899 | event-prop | onClick | {() => { setSelectedRoleNames([role.name]); setBulkRoleActionModal('delete'); }} |
| UI-0225 | 1935 | control | button | <button type="button" onClick={() => handleToggleRole(role.name)} className="text-slate-400 hover:text-amber-600 cursor-pointer pt-0.5" > {isSelected ? ( <CheckSquare className="w-4 h-4 text-amber-600" /> ) : ( <Square className="w-4 h-4 te |
| UI-0226 | 1937 | event-prop | onClick | {() => handleToggleRole(role.name)} |
| UI-0227 | 1959 | control | button | <button type="button" onClick={() => { setSelectedRoleNames([role.name]); setBulkRoleTargetUsers([]); setBulkRoleAssignMode('add'); setBulkRoleActionModal('assignToUsers'); }} className="text-[10.5px] font-mono font-bold px-2 py-0.5 rounded |
| UI-0228 | 1961 | event-prop | onClick | {() => { setSelectedRoleNames([role.name]); setBulkRoleTargetUsers([]); setBulkRoleAssignMode('add'); setBulkRoleActionModal('assignToUsers'); }} |
| UI-0229 | 1981 | event-prop | onClick | {() => { setSelectedRoleNames([role.name]); setBulkRoleActionModal('grantPerms'); }} |
| UI-0230 | 2032 | control | button | <button onClick={handleBulkExportPerms} className="flex items-center gap-1.5 px-3 py-1.5 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-200 rounded-lg text-xs font-semibold shadow-2x |
| UI-0231 | 2033 | event-prop | onClick | {handleBulkExportPerms} |
| UI-0232 | 2061 | control | button | <button onClick={() => setBulkPermActionModal('assignToRole')} className="flex items-center gap-1 px-2.5 py-1 bg-purple-700 hover:bg-purple-800 text-white rounded-lg text-xs font-semibold transition-all cursor-pointer shadow-2xs" title="Gán |
| UI-0233 | 2062 | event-prop | onClick | {() => setBulkPermActionModal('assignToRole')} |
| UI-0234 | 2070 | control | button | <button onClick={() => setBulkPermActionModal('revokeFromRole')} className="flex items-center gap-1 px-2.5 py-1 bg-white dark:bg-slate-900 text-rose-700 dark:text-rose-400 hover:bg-rose-50 dark:hover:bg-rose-950/40 border border-rose-200 da |
| UI-0235 | 2071 | event-prop | onClick | {() => setBulkPermActionModal('revokeFromRole')} |
| UI-0236 | 2079 | control | button | <button onClick={() => { setBulkPermTargetUsers([]); setBulkPermDirectMode('grant'); setBulkPermActionModal('assignToUsers'); }} className="flex items-center gap-1 px-2.5 py-1 bg-white dark:bg-slate-900 text-purple-700 dark:text-purple-300  |
| UI-0237 | 2080 | event-prop | onClick | {() => { setBulkPermTargetUsers([]); setBulkPermDirectMode('grant'); setBulkPermActionModal('assignToUsers'); }} |
| UI-0238 | 2092 | control | button | <button onClick={() => handleBulkToggleAuditLogging(true)} className="flex items-center gap-1 px-2.5 py-1 bg-white dark:bg-slate-900 text-emerald-700 dark:text-emerald-400 hover:bg-emerald-50 dark:hover:bg-emerald-950/40 border border-emera |
| UI-0239 | 2093 | event-prop | onClick | {() => handleBulkToggleAuditLogging(true)} |
| UI-0240 | 2101 | control | button | <button onClick={() => handleBulkToggleAuditLogging(false)} className="flex items-center gap-1 px-2.5 py-1 bg-white dark:bg-slate-900 text-slate-700 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 border border-slate-300 dark |
| UI-0241 | 2102 | event-prop | onClick | {() => handleBulkToggleAuditLogging(false)} |
| UI-0242 | 2109 | control | button | <button onClick={handleBulkExportPerms} className="flex items-center gap-1 px-2.5 py-1 bg-white dark:bg-slate-900 text-slate-800 dark:text-slate-100 hover:bg-slate-100 dark:hover:bg-slate-800 border border-slate-300 dark:border-slate-700 ro |
| UI-0243 | 2110 | event-prop | onClick | {handleBulkExportPerms} |
| UI-0244 | 2117 | control | button | <button onClick={() => setSelectedPermIds([])} className="p-1 text-slate-500 hover:text-slate-800 dark:hover:text-slate-200 rounded-md hover:bg-slate-200/60 dark:hover:bg-slate-800 transition-colors ml-1 cursor-pointer" title="Bỏ chọn tất c |
| UI-0245 | 2118 | event-prop | onClick | {() => setSelectedPermIds([])} |
| UI-0246 | 2131 | control | input | <input type="text" placeholder="Tìm theo mã quyền hạn, mô tả, module..." value={permSearchTerm} onChange={(e) => setPermSearchTerm(e.target.value)} className="w-full pl-8 pr-3 py-1.5 text-xs bg-slate-50 dark:bg-slate-800/60 border border-sl |
| UI-0247 | 2135 | event-prop | onChange | {(e) => setPermSearchTerm(e.target.value)} |
| UI-0248 | 2144 | control | button | <button key={mod} onClick={() => setPermModuleFilter(mod)} className={&#96;px-2.5 py-1 rounded-lg text-xs font-semibold cursor-pointer transition-all ${ permModuleFilter === mod ? 'bg-purple-700 text-white shadow-xs' : 'bg-slate-100 dark:bg |
| UI-0249 | 2146 | event-prop | onClick | {() => setPermModuleFilter(mod)} |
| UI-0250 | 2166 | control | button | <button type="button" onClick={handleToggleSelectAllPerms} className="text-slate-600 dark:text-slate-300 hover:text-purple-600 cursor-pointer" title="Chọn tất cả quyền đang hiển thị" > {filteredPerms.length > 0 && filteredPerms.every(p => s |
| UI-0251 | 2168 | event-prop | onClick | {handleToggleSelectAllPerms} |
| UI-0252 | 2201 | control | button | <button type="button" onClick={() => handleTogglePerm(perm.id)} className="text-slate-400 hover:text-purple-600 cursor-pointer" > {isSelected ? ( <CheckSquare className="w-4 h-4 text-purple-600" /> ) : ( <Square className="w- |
| UI-0253 | 2203 | event-prop | onClick | {() => handleTogglePerm(perm.id)} |
| UI-0254 | 2225 | control | button | <button type="button" onClick={() => { setPermissionsList(prev => prev.map(item => item.id === perm.id ? { ...item, auditLogged: !item.auditLogged } : item )); showToast(&#96;Đã ${!perm.auditLogged ? 'kích hoạt' : 'tắt'} Audit Trace cho "${ |
| UI-0255 | 2227 | event-prop | onClick | {() => { setPermissionsList(prev => prev.map(item => item.id === perm.id ? { ...item, auditLogged: !item.auditLogged } : item )); showToast(&#96;Đã ${!perm.auditLogged ? 'kích hoạt' : 'tắt'} Audit Trace cho "${perm.name}"!&#96;); }} |
| UI-0256 | 2285 | control | button | <button onClick={handleBulkPingResources} className="flex items-center gap-1.5 px-3 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-xs font-semibold shadow-xs cursor-pointer" > <Activity className="w-3.5 h-3.5" /> <span>Heal |
| UI-0257 | 2286 | event-prop | onClick | {handleBulkPingResources} |
| UI-0258 | 2320 | control | button | <button onClick={() => handleBulkSetResourceStatus('ONLINE')} className="px-2.5 py-1 bg-white dark:bg-slate-900 text-emerald-700 dark:text-emerald-400 border border-emerald-300 dark:border-emerald-800 rounded font-semibold text-xs cursor-po |
| UI-0259 | 2321 | event-prop | onClick | {() => handleBulkSetResourceStatus('ONLINE')} |
| UI-0260 | 2326 | control | button | <button onClick={() => handleBulkSetResourceStatus('MAINTENANCE')} className="px-2.5 py-1 bg-white dark:bg-slate-900 text-amber-700 dark:text-amber-400 border border-amber-300 dark:border-amber-800 rounded font-semibold text-xs cursor-point |
| UI-0261 | 2327 | event-prop | onClick | {() => handleBulkSetResourceStatus('MAINTENANCE')} |
| UI-0262 | 2332 | control | button | <button onClick={() => handleBulkSetResourceStatus('OFFLINE')} className="px-2.5 py-1 bg-white dark:bg-slate-900 text-rose-700 dark:text-rose-400 border border-rose-300 dark:border-rose-800 rounded font-semibold text-xs cursor-pointer shado |
| UI-0263 | 2333 | event-prop | onClick | {() => handleBulkSetResourceStatus('OFFLINE')} |
| UI-0264 | 2338 | control | button | <button onClick={handleBulkPingResources} className="px-2.5 py-1 bg-blue-600 hover:bg-blue-700 text-white rounded font-semibold text-xs cursor-pointer shadow-2xs flex items-center gap-1" > <Activity className="w-3 h-3" /> <span>Ping Check L |
| UI-0265 | 2339 | event-prop | onClick | {handleBulkPingResources} |
| UI-0266 | 2345 | control | button | <button onClick={handleBulkExportResources} className="px-2.5 py-1 bg-white dark:bg-slate-900 text-slate-700 dark:text-slate-300 border border-slate-300 dark:border-slate-700 rounded font-semibold text-xs cursor-pointer shadow-2xs" > Xuất C |
| UI-0267 | 2346 | event-prop | onClick | {handleBulkExportResources} |
| UI-0268 | 2351 | control | button | <button onClick={() => setSelectedResourceIds([])} className="p-1 text-slate-400 hover:text-slate-600 cursor-pointer" title="Bỏ chọn" > <X className="w-3.5 h-3.5" />  |
| UI-0269 | 2352 | event-prop | onClick | {() => setSelectedResourceIds([])} |
| UI-0270 | 2368 | control | button | <button type="button" onClick={handleToggleSelectAllResources} className="text-slate-600 dark:text-slate-300 hover:text-blue-600 cursor-pointer" > {resourcesList.length > 0 && resourcesList.every(r => selectedResourceIds.includes(r.id)) ? ( |
| UI-0271 | 2370 | event-prop | onClick | {handleToggleSelectAllResources} |
| UI-0272 | 2403 | control | button | <button type="button" onClick={() => handleToggleResource(res.id)} className="text-slate-400 hover:text-blue-600 cursor-pointer" > {isSelected ? ( <CheckSquare className="w-4 h-4 text-blue-600" /> ) : ( <Square className="w-4  |
| UI-0273 | 2405 | event-prop | onClick | {() => handleToggleResource(res.id)} |
| UI-0274 | 2474 | control | button | <button onClick={handleBulkRotateCertificates} className="px-2.5 py-1 bg-orange-600 hover:bg-orange-700 text-white rounded font-semibold text-xs cursor-pointer shadow-2xs flex items-center gap-1" title="Tạo mới thumbprint và xoay vòng chứng |
| UI-0275 | 2475 | event-prop | onClick | {handleBulkRotateCertificates} |
| UI-0276 | 2482 | control | button | <button onClick={handleBulkRenewCertificates} className="px-2.5 py-1 bg-white dark:bg-slate-900 text-slate-800 dark:text-slate-200 border border-slate-300 dark:border-slate-700 rounded font-semibold text-xs cursor-pointer shadow-2xs hover:b |
| UI-0277 | 2483 | event-prop | onClick | {handleBulkRenewCertificates} |
| UI-0278 | 2488 | control | button | <button onClick={handleBulkRevokeServiceAccounts} className="px-2.5 py-1 bg-rose-50 dark:bg-rose-950/40 text-rose-700 dark:text-rose-400 border border-rose-300 dark:border-rose-800 rounded font-semibold text-xs cursor-pointer shadow-2xs" >  |
| UI-0279 | 2489 | event-prop | onClick | {handleBulkRevokeServiceAccounts} |
| UI-0280 | 2494 | control | button | <button onClick={handleBulkExportServiceAccounts} className="px-2.5 py-1 bg-white dark:bg-slate-900 text-slate-700 dark:text-slate-300 border border-slate-300 dark:border-slate-700 rounded font-semibold text-xs cursor-pointer shadow-2xs" >  |
| UI-0281 | 2495 | event-prop | onClick | {handleBulkExportServiceAccounts} |
| UI-0282 | 2500 | control | button | <button onClick={() => setSelectedSaIds([])} className="p-1 text-slate-400 hover:text-slate-600 cursor-pointer" title="Bỏ chọn" > <X className="w-3.5 h-3.5" />  |
| UI-0283 | 2501 | event-prop | onClick | {() => setSelectedSaIds([])} |
| UI-0284 | 2517 | control | button | <button type="button" onClick={handleToggleSelectAllSa} className="text-slate-600 dark:text-slate-300 hover:text-orange-600 cursor-pointer" > {serviceAccountsList.length > 0 && serviceAccountsList.every(s => selectedSaIds.includes(s.id)) ?  |
| UI-0285 | 2519 | event-prop | onClick | {handleToggleSelectAllSa} |
| UI-0286 | 2552 | control | button | <button type="button" onClick={() => handleToggleSa(sa.id)} className="text-slate-400 hover:text-orange-600 cursor-pointer" > {isSelected ? ( <CheckSquare className="w-4 h-4 text-orange-600" /> ) : ( <Square className="w- |
| UI-0287 | 2554 | event-prop | onClick | {() => handleToggleSa(sa.id)} |
| UI-0288 | 2675 | control | button | <button onClick={() => setShowAddUserModal(false)} className="text-slate-400 hover:text-slate-600 cursor-pointer text-sm" > ✕  |
| UI-0289 | 2676 | event-prop | onClick | {() => setShowAddUserModal(false)} |
| UI-0290 | 2685 | control | input | <input type="text" value={newUsername} onChange={(e) => setNewUsername(e.target.value)} placeholder="e.g. hungnv" className="w-full px-2.5 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded focus:outline |
| UI-0291 | 2688 | event-prop | onChange | {(e) => setNewUsername(e.target.value)} |
| UI-0292 | 2695 | control | input | <input type="text" value={newFullName} onChange={(e) => setNewFullName(e.target.value)} placeholder="e.g. Nguyễn Văn Hùng" className="w-full px-2.5 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded focu |
| UI-0293 | 2698 | event-prop | onChange | {(e) => setNewFullName(e.target.value)} |
| UI-0294 | 2705 | control | input | <input type="email" value={newEmail} onChange={(e) => setNewEmail(e.target.value)} placeholder="hungnv@ecmbpm.vn" className="w-full px-2.5 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded focus:outline |
| UI-0295 | 2708 | event-prop | onChange | {(e) => setNewEmail(e.target.value)} |
| UI-0296 | 2715 | control | select | <select value={newRole} onChange={(e) => setNewRole(e.target.value)} className="w-full px-2.5 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded font-semibold cursor-pointer" > <option value="NCLOS B CO" |
| UI-0297 | 2717 | event-prop | onChange | {(e) => setNewRole(e.target.value)} |
| UI-0298 | 2730 | control | button | <button onClick={() => setShowAddUserModal(false)} className="px-3 py-1.5 border border-slate-300 dark:border-slate-700 rounded text-xs font-semibold cursor-pointer" > Hủy  |
| UI-0299 | 2731 | event-prop | onClick | {() => setShowAddUserModal(false)} |
| UI-0300 | 2736 | control | button | <button onClick={handleCreateUser} className="px-4 py-1.5 bg-emerald-700 text-white rounded text-xs font-semibold hover:bg-emerald-800 cursor-pointer shadow-xs" > Lưu Người Dùng  |
| UI-0301 | 2737 | event-prop | onClick | {handleCreateUser} |
| UI-0302 | 2765 | control | button | <button onClick={() => handleBulkLock('LOCKED')} className="flex items-center gap-1 px-2.5 py-1.5 bg-rose-500/20 hover:bg-rose-500/30 text-rose-300 border border-rose-500/30 rounded-lg text-xs font-semibold transition-all cursor-pointer" ti |
| UI-0303 | 2766 | event-prop | onClick | {() => handleBulkLock('LOCKED')} |
| UI-0304 | 2774 | control | button | <button onClick={() => handleBulkLock('ACTIVE')} className="flex items-center gap-1 px-2.5 py-1.5 bg-emerald-500/20 hover:bg-emerald-500/30 text-emerald-300 border border-emerald-500/30 rounded-lg text-xs font-semibold transition-all cursor |
| UI-0305 | 2775 | event-prop | onClick | {() => handleBulkLock('ACTIVE')} |
| UI-0306 | 2783 | control | button | <button onClick={() => setBulkActionModal('roles')} className="flex items-center gap-1 px-2.5 py-1.5 bg-slate-800 hover:bg-slate-700 text-slate-200 border border-slate-700 rounded-lg text-xs font-semibold transition-all cursor-pointer" titl |
| UI-0307 | 2784 | event-prop | onClick | {() => setBulkActionModal('roles')} |
| UI-0308 | 2792 | control | button | <button onClick={() => setBulkActionModal('permissions')} className="flex items-center gap-1 px-2.5 py-1.5 bg-emerald-700 hover:bg-emerald-600 text-white border border-emerald-500/40 rounded-lg text-xs font-semibold transition-all cursor-po |
| UI-0309 | 2793 | event-prop | onClick | {() => setBulkActionModal('permissions')} |
| UI-0310 | 2801 | control | button | <button onClick={() => setBulkActionModal('department')} className="flex items-center gap-1 px-2.5 py-1.5 bg-slate-800 hover:bg-slate-700 text-slate-200 border border-slate-700 rounded-lg text-xs font-semibold transition-all cursor-pointer" |
| UI-0311 | 2802 | event-prop | onClick | {() => setBulkActionModal('department')} |
| UI-0312 | 2810 | control | button | <button onClick={() => setBulkActionModal('security')} className="flex items-center gap-1 px-2.5 py-1.5 bg-slate-800 hover:bg-slate-700 text-slate-200 border border-slate-700 rounded-lg text-xs font-semibold transition-all cursor-pointer" t |
| UI-0313 | 2811 | event-prop | onClick | {() => setBulkActionModal('security')} |
| UI-0314 | 2819 | control | button | <button onClick={handleBulkExportCsv} className="flex items-center gap-1 px-2.5 py-1.5 bg-slate-800 hover:bg-slate-700 text-slate-200 border border-slate-700 rounded-lg text-xs font-semibold transition-all cursor-pointer" title="Xuất file C |
| UI-0315 | 2820 | event-prop | onClick | {handleBulkExportCsv} |
| UI-0316 | 2828 | control | button | <button onClick={() => setBulkActionModal('delete')} className="flex items-center gap-1 px-2.5 py-1.5 bg-rose-600 hover:bg-rose-700 text-white rounded-lg text-xs font-semibold transition-all cursor-pointer shadow-xs" title="Xóa hoặc lưu trữ |
| UI-0317 | 2829 | event-prop | onClick | {() => setBulkActionModal('delete')} |
| UI-0318 | 2837 | control | button | <button onClick={handleClearSelection} className="p-1.5 text-slate-400 hover:text-white rounded-lg hover:bg-slate-800 transition-colors cursor-pointer ml-1" title="Bỏ chọn tất cả" > <X className="w-4 h-4" />  |
| UI-0319 | 2838 | event-prop | onClick | {handleClearSelection} |
| UI-0320 | 2863 | control | button | <button onClick={() => setBulkActionModal(null)} className="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer" > ✕  |
| UI-0321 | 2864 | event-prop | onClick | {() => setBulkActionModal(null)} |
| UI-0322 | 2895 | control | button | <button type="button" onClick={() => setBulkRoleMode('add')} className={&#96;p-2.5 rounded-xl border text-center transition-all cursor-pointer ${ bulkRoleMode === 'add' ? 'border-emerald-500 bg-emerald-50/70 dark:bg-emerald-950/40 text-emer |
| UI-0323 | 2897 | event-prop | onClick | {() => setBulkRoleMode('add')} |
| UI-0324 | 2908 | control | button | <button type="button" onClick={() => setBulkRoleMode('replace')} className={&#96;p-2.5 rounded-xl border text-center transition-all cursor-pointer ${ bulkRoleMode === 'replace' ? 'border-emerald-500 bg-emerald-50/70 dark:bg-emerald-950/40 t |
| UI-0325 | 2910 | event-prop | onClick | {() => setBulkRoleMode('replace')} |
| UI-0326 | 2921 | control | button | <button type="button" onClick={() => setBulkRoleMode('remove')} className={&#96;p-2.5 rounded-xl border text-center transition-all cursor-pointer ${ bulkRoleMode === 'remove' ? 'border-rose-500 bg-rose-50/70 dark:bg-rose-950/40 text-rose-80 |
| UI-0327 | 2923 | event-prop | onClick | {() => setBulkRoleMode('remove')} |
| UI-0328 | 2941 | control | select | <select value={bulkTargetRole} onChange={(e) => setBulkTargetRole(e.target.value)} className="w-full px-3 py-2 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg font-semibold text-xs cursor-pointer focus:ou |
| UI-0329 | 2943 | event-prop | onChange | {(e) => setBulkTargetRole(e.target.value)} |
| UI-0330 | 2959 | control | button | <button onClick={() => setBulkActionModal(null)} className="px-3.5 py-1.5 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-800" > Hủy  |
| UI-0331 | 2960 | event-prop | onClick | {() => setBulkActionModal(null)} |
| UI-0332 | 2965 | control | button | <button onClick={handleBulkApplyRoles} className="px-4 py-1.5 bg-emerald-700 text-white rounded-lg text-xs font-semibold hover:bg-emerald-800 cursor-pointer shadow-xs" > Xác Nhận Cập Nhật  |
| UI-0333 | 2966 | event-prop | onClick | {handleBulkApplyRoles} |
| UI-0334 | 2991 | control | button | <button onClick={() => setBulkActionModal(null)} className="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer" > ✕  |
| UI-0335 | 2992 | event-prop | onClick | {() => setBulkActionModal(null)} |
| UI-0336 | 3020 | control | select | <select value={bulkTargetDept} onChange={(e) => setBulkTargetDept(e.target.value)} className="w-full px-3 py-2 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg font-semibold text-xs cursor-pointer focus:ou |
| UI-0337 | 3022 | event-prop | onChange | {(e) => setBulkTargetDept(e.target.value)} |
| UI-0338 | 3038 | control | button | <button onClick={() => setBulkActionModal(null)} className="px-3.5 py-1.5 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-800" > Hủy  |
| UI-0339 | 3039 | event-prop | onClick | {() => setBulkActionModal(null)} |
| UI-0340 | 3044 | control | button | <button onClick={handleBulkApplyDept} className="px-4 py-1.5 bg-blue-600 text-white rounded-lg text-xs font-semibold hover:bg-blue-700 cursor-pointer shadow-xs" > Xác Nhận Điều Chuyển  |
| UI-0341 | 3045 | event-prop | onClick | {handleBulkApplyDept} |
| UI-0342 | 3070 | control | button | <button onClick={() => setBulkActionModal(null)} className="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer" > ✕  |
| UI-0343 | 3071 | event-prop | onClick | {() => setBulkActionModal(null)} |
| UI-0344 | 3085 | control | input | <input type="checkbox" checked={bulkResetPw} onChange={(e) => setBulkResetPw(e.target.checked)} className="mt-0.5 rounded text-emerald-600 focus:ring-emerald-500" /> <div> <span className="font-bold text-slate-900 dark:text-slate-100 block" |
| UI-0345 | 3088 | event-prop | onChange | {(e) => setBulkResetPw(e.target.checked)} |
| UI-0346 | 3102 | control | input | <input type="checkbox" checked={bulkEnforce2fa} onChange={(e) => setBulkEnforce2fa(e.target.checked)} className="mt-0.5 rounded text-emerald-600 focus:ring-emerald-500" /> <div> <span className="font-bold text-slate-900 dark:text-slate-100  |
| UI-0347 | 3105 | event-prop | onChange | {(e) => setBulkEnforce2fa(e.target.checked)} |
| UI-0348 | 3119 | control | input | <input type="checkbox" checked={bulkRevokeSessions} onChange={(e) => setBulkRevokeSessions(e.target.checked)} className="mt-0.5 rounded text-emerald-600 focus:ring-emerald-500" /> <div> <span className="font-bold text-slate-900 dark:text-sl |
| UI-0349 | 3122 | event-prop | onChange | {(e) => setBulkRevokeSessions(e.target.checked)} |
| UI-0350 | 3138 | control | button | <button onClick={() => setBulkActionModal(null)} className="px-3.5 py-1.5 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-800" > Hủy  |
| UI-0351 | 3139 | event-prop | onClick | {() => setBulkActionModal(null)} |
| UI-0352 | 3144 | control | button | <button onClick={handleBulkApplySecurity} className="px-4 py-1.5 bg-purple-700 text-white rounded-lg text-xs font-semibold hover:bg-purple-800 cursor-pointer shadow-xs" > Thực Thi Bảo Mật  |
| UI-0353 | 3145 | event-prop | onClick | {handleBulkApplySecurity} |
| UI-0354 | 3170 | control | button | <button onClick={() => setBulkActionModal(null)} className="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer" > ✕  |
| UI-0355 | 3171 | event-prop | onClick | {() => setBulkActionModal(null)} |
| UI-0356 | 3198 | control | button | <button onClick={() => setBulkActionModal(null)} className="px-3.5 py-1.5 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-800" > Hủy  |
| UI-0357 | 3199 | event-prop | onClick | {() => setBulkActionModal(null)} |
| UI-0358 | 3204 | control | button | <button onClick={() => handleBulkDelete(false)} className="px-3.5 py-1.5 bg-slate-800 hover:bg-slate-700 text-white rounded-lg text-xs font-semibold cursor-pointer shadow-xs" > Lưu Trữ (Archive)  |
| UI-0359 | 3205 | event-prop | onClick | {() => handleBulkDelete(false)} |
| UI-0360 | 3210 | control | button | <button onClick={() => handleBulkDelete(true)} className="px-4 py-1.5 bg-rose-600 hover:bg-rose-700 text-white rounded-lg text-xs font-semibold cursor-pointer shadow-xs" > Xóa Vĩnh Viễn  |
| UI-0361 | 3211 | event-prop | onClick | {() => handleBulkDelete(true)} |
| UI-0362 | 3241 | control | button | <button onClick={() => setBulkActionModal(null)} className="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer" > ✕  |
| UI-0363 | 3242 | event-prop | onClick | {() => setBulkActionModal(null)} |
| UI-0364 | 3253 | control | button | <button type="button" onClick={() => setBulkPermMode('grant')} className={&#96;px-3 py-1 rounded-md font-semibold cursor-pointer transition-all ${ bulkPermMode === 'grant' ? 'bg-emerald-700 text-white shadow-xs' : 'text-slate-600 dark:text- |
| UI-0365 | 3255 | event-prop | onClick | {() => setBulkPermMode('grant')} |
| UI-0366 | 3264 | control | button | <button type="button" onClick={() => setBulkPermMode('revoke')} className={&#96;px-3 py-1 rounded-md font-semibold cursor-pointer transition-all ${ bulkPermMode === 'revoke' ? 'bg-rose-600 text-white shadow-xs' : 'text-slate-600 dark:text-s |
| UI-0367 | 3266 | event-prop | onClick | {() => setBulkPermMode('revoke')} |
| UI-0368 | 3284 | control | button | <button type="button" onClick={() => setBulkSelectedPerms(INITIAL_PERMISSIONS_CATALOGUE.map(p => p.name))} className="text-emerald-700 dark:text-emerald-400 hover:underline text-[11px] font-semibold cursor-pointer" > Chọn tất cả  |
| UI-0369 | 3286 | event-prop | onClick | {() => setBulkSelectedPerms(INITIAL_PERMISSIONS_CATALOGUE.map(p => p.name))} |
| UI-0370 | 3292 | control | button | <button type="button" onClick={() => setBulkSelectedPerms([])} className="text-slate-400 hover:underline text-[11px] cursor-pointer" > Bỏ chọn  |
| UI-0371 | 3294 | event-prop | onClick | {() => setBulkSelectedPerms([])} |
| UI-0372 | 3312 | control | input | <input type="checkbox" checked={isChecked} onChange={(e) => { if (e.target.checked) { setBulkSelectedPerms(prev => [...prev, perm.name]); } else { setBulkSelectedPerms(prev => prev.filter(p => p !== perm.name)); } }} className="mt-0.5 round |
| UI-0373 | 3315 | event-prop | onChange | {(e) => { if (e.target.checked) { setBulkSelectedPerms(prev => [...prev, perm.name]); } else { setBulkSelectedPerms(prev => prev.filter(p => p !== perm.name)); } }} |
| UI-0374 | 3345 | control | button | <button onClick={() => setBulkActionModal(null)} className="px-3.5 py-1.5 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-800" > Hủy  |
| UI-0375 | 3346 | event-prop | onClick | {() => setBulkActionModal(null)} |
| UI-0376 | 3351 | control | button | <button onClick={handleBulkApplyPermissions} className="px-4 py-1.5 bg-emerald-700 text-white rounded-lg text-xs font-semibold hover:bg-emerald-800 cursor-pointer shadow-xs" > Xác Nhận {bulkPermMode === 'grant' ? 'Gán Quyền' : 'Thu Hồi'} ({ |
| UI-0377 | 3352 | event-prop | onClick | {handleBulkApplyPermissions} |
| UI-0378 | 3382 | control | button | <button onClick={() => setBulkRoleActionModal(null)} className="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer" > ✕  |
| UI-0379 | 3383 | event-prop | onClick | {() => setBulkRoleActionModal(null)} |
| UI-0380 | 3397 | control | button | <button type="button" onClick={() => setBulkRoleTargetPerms(INITIAL_PERMISSIONS_CATALOGUE.map(p => p.name))} className="text-amber-700 dark:text-amber-400 hover:underline text-[11px] font-semibold cursor-pointer" > Chọn tất cả  |
| UI-0381 | 3399 | event-prop | onClick | {() => setBulkRoleTargetPerms(INITIAL_PERMISSIONS_CATALOGUE.map(p => p.name))} |
| UI-0382 | 3405 | control | button | <button type="button" onClick={() => setBulkRoleTargetPerms([])} className="text-slate-400 hover:underline text-[11px] cursor-pointer" > Bỏ chọn  |
| UI-0383 | 3407 | event-prop | onClick | {() => setBulkRoleTargetPerms([])} |
| UI-0384 | 3425 | control | input | <input type="checkbox" checked={isChecked} onChange={(e) => { if (e.target.checked) { setBulkRoleTargetPerms(prev => [...prev, perm.name]); } else { setBulkRoleTargetPerms(prev => prev.filter(p => p !== perm.name)); } }} className="mt-0.5 r |
| UI-0385 | 3428 | event-prop | onChange | {(e) => { if (e.target.checked) { setBulkRoleTargetPerms(prev => [...prev, perm.name]); } else { setBulkRoleTargetPerms(prev => prev.filter(p => p !== perm.name)); } }} |
| UI-0386 | 3453 | control | button | <button onClick={() => setBulkRoleActionModal(null)} className="px-3.5 py-1.5 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-800" > Hủy  |
| UI-0387 | 3454 | event-prop | onClick | {() => setBulkRoleActionModal(null)} |
| UI-0388 | 3459 | control | button | <button onClick={() => handleBulkGrantPermsToRoles('revoke')} className="px-3.5 py-1.5 border border-rose-300 text-rose-700 dark:text-rose-300 rounded-lg text-xs font-semibold cursor-pointer hover:bg-rose-50" > Thu Hồi Khỏi Roles  |
| UI-0389 | 3460 | event-prop | onClick | {() => handleBulkGrantPermsToRoles('revoke')} |
| UI-0390 | 3465 | control | button | <button onClick={() => handleBulkGrantPermsToRoles('grant')} className="px-4 py-1.5 bg-amber-600 hover:bg-amber-700 text-white rounded-lg text-xs font-semibold cursor-pointer shadow-xs" > Gán Bổ Sung ({bulkRoleTargetPerms.length})  |
| UI-0391 | 3466 | event-prop | onClick | {() => handleBulkGrantPermsToRoles('grant')} |
| UI-0392 | 3491 | control | button | <button onClick={() => setBulkRoleActionModal(null)} className="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer" > ✕  |
| UI-0393 | 3492 | event-prop | onClick | {() => setBulkRoleActionModal(null)} |
| UI-0394 | 3508 | control | select | <select value={bulkRoleTargetType} onChange={(e) => setBulkRoleTargetType(e.target.value)} className="w-full px-3 py-2 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg font-semibold text-xs cursor-pointer  |
| UI-0395 | 3510 | event-prop | onChange | {(e) => setBulkRoleTargetType(e.target.value)} |
| UI-0396 | 3523 | control | button | <button onClick={() => setBulkRoleActionModal(null)} className="px-3.5 py-1.5 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-800" > Hủy  |
| UI-0397 | 3524 | event-prop | onClick | {() => setBulkRoleActionModal(null)} |
| UI-0398 | 3529 | control | button | <button onClick={handleBulkChangeRolesType} className="px-4 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-xs font-semibold cursor-pointer shadow-xs" > Xác Nhận Đổi Nhóm  |
| UI-0399 | 3530 | event-prop | onClick | {handleBulkChangeRolesType} |
| UI-0400 | 3555 | control | button | <button onClick={() => setBulkRoleActionModal(null)} className="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer" > ✕  |
| UI-0401 | 3556 | event-prop | onClick | {() => setBulkRoleActionModal(null)} |
| UI-0402 | 3580 | control | button | <button onClick={() => setBulkRoleActionModal(null)} className="px-3.5 py-1.5 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-800" > Hủy  |
| UI-0403 | 3581 | event-prop | onClick | {() => setBulkRoleActionModal(null)} |
| UI-0404 | 3586 | control | button | <button onClick={handleBulkDeleteRoles} className="px-4 py-1.5 bg-rose-600 hover:bg-rose-700 text-white rounded-lg text-xs font-semibold cursor-pointer shadow-xs" > Xác Nhận Xóa  |
| UI-0405 | 3587 | event-prop | onClick | {handleBulkDeleteRoles} |
| UI-0406 | 3618 | control | button | <button onClick={() => setBulkPermActionModal(null)} className="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer" > ✕  |
| UI-0407 | 3619 | event-prop | onClick | {() => setBulkPermActionModal(null)} |
| UI-0408 | 3635 | control | select | <select value={bulkPermTargetRole} onChange={(e) => setBulkPermTargetRole(e.target.value)} className="w-full px-3 py-2 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg font-semibold text-xs cursor-pointer  |
| UI-0409 | 3637 | event-prop | onChange | {(e) => setBulkPermTargetRole(e.target.value)} |
| UI-0410 | 3650 | control | button | <button onClick={() => setBulkPermActionModal(null)} className="px-3.5 py-1.5 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-800" > Hủy  |
| UI-0411 | 3651 | event-prop | onClick | {() => setBulkPermActionModal(null)} |
| UI-0412 | 3656 | control | button | <button onClick={bulkPermActionModal === 'assignToRole' ? handleBulkAssignPermsToRole : handleBulkRevokePermsFromRole} className={&#96;px-4 py-1.5 text-white rounded-lg text-xs font-semibold cursor-pointer shadow-xs ${ bulkPermActionModal = |
| UI-0413 | 3657 | event-prop | onClick | {bulkPermActionModal === 'assignToRole' ? handleBulkAssignPermsToRole : handleBulkRevokePermsFromRole} |
| UI-0414 | 3689 | control | button | <button onClick={() => setBulkRoleActionModal(null)} className="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer" > ✕  |
| UI-0415 | 3690 | event-prop | onClick | {() => setBulkRoleActionModal(null)} |
| UI-0416 | 3704 | control | button | <button type="button" onClick={() => setBulkRoleAssignMode('add')} className={&#96;p-2 rounded-lg border text-center transition-all cursor-pointer ${ bulkRoleAssignMode === 'add' ? 'border-amber-500 bg-amber-50 dark:bg-amber-950/50 text-amb |
| UI-0417 | 3706 | event-prop | onClick | {() => setBulkRoleAssignMode('add')} |
| UI-0418 | 3716 | control | button | <button type="button" onClick={() => setBulkRoleAssignMode('remove')} className={&#96;p-2 rounded-lg border text-center transition-all cursor-pointer ${ bulkRoleAssignMode === 'remove' ? 'border-rose-500 bg-rose-50 dark:bg-rose-950/50 text- |
| UI-0419 | 3718 | event-prop | onClick | {() => setBulkRoleAssignMode('remove')} |
| UI-0420 | 3728 | control | button | <button type="button" onClick={() => setBulkRoleAssignMode('replace')} className={&#96;p-2 rounded-lg border text-center transition-all cursor-pointer ${ bulkRoleAssignMode === 'replace' ? 'border-blue-500 bg-blue-50 dark:bg-blue-950/50 tex |
| UI-0421 | 3730 | event-prop | onClick | {() => setBulkRoleAssignMode('replace')} |
| UI-0422 | 3750 | control | button | <button type="button" onClick={() => setBulkRoleTargetUsers(userList.map(u => u.id))} className="text-amber-700 dark:text-amber-400 hover:underline text-[11px] font-semibold cursor-pointer" > Chọn tất cả  |
| UI-0423 | 3752 | event-prop | onClick | {() => setBulkRoleTargetUsers(userList.map(u => u.id))} |
| UI-0424 | 3758 | control | button | <button type="button" onClick={() => setBulkRoleTargetUsers([])} className="text-slate-400 hover:underline text-[11px] cursor-pointer" > Bỏ chọn  |
| UI-0425 | 3760 | event-prop | onClick | {() => setBulkRoleTargetUsers([])} |
| UI-0426 | 3778 | control | input | <input type="checkbox" checked={isChecked} onChange={(e) => { if (e.target.checked) { setBulkRoleTargetUsers(prev => [...prev, user.id]); } else { setBulkRoleTargetUsers(prev => prev.filter(id => id !== user.id)); } }} className="rounded te |
| UI-0427 | 3781 | event-prop | onChange | {(e) => { if (e.target.checked) { setBulkRoleTargetUsers(prev => [...prev, user.id]); } else { setBulkRoleTargetUsers(prev => prev.filter(id => id !== user.id)); } }} |
| UI-0428 | 3818 | control | button | <button onClick={() => setBulkRoleActionModal(null)} className="px-3.5 py-1.5 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-800" > Hủy  |
| UI-0429 | 3819 | event-prop | onClick | {() => setBulkRoleActionModal(null)} |
| UI-0430 | 3824 | control | button | <button onClick={handleBulkUpdateRoleAssignmentsToUsers} disabled={bulkRoleTargetUsers.length === 0} className="px-4 py-1.5 bg-amber-600 hover:bg-amber-700 disabled:opacity-50 text-white rounded-lg text-xs font-semibold cursor-pointer shado |
| UI-0431 | 3825 | event-prop | onClick | {handleBulkUpdateRoleAssignmentsToUsers} |
| UI-0432 | 3856 | control | button | <button onClick={() => setBulkPermActionModal(null)} className="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer" > ✕  |
| UI-0433 | 3857 | event-prop | onClick | {() => setBulkPermActionModal(null)} |
| UI-0434 | 3870 | control | button | <button type="button" onClick={() => setBulkPermDirectMode('grant')} className={&#96;p-2 rounded-lg border text-center transition-all cursor-pointer ${ bulkPermDirectMode === 'grant' ? 'border-purple-600 bg-purple-50 dark:bg-purple-950/50 t |
| UI-0435 | 3872 | event-prop | onClick | {() => setBulkPermDirectMode('grant')} |
| UI-0436 | 3882 | control | button | <button type="button" onClick={() => setBulkPermDirectMode('revoke')} className={&#96;p-2 rounded-lg border text-center transition-all cursor-pointer ${ bulkPermDirectMode === 'revoke' ? 'border-rose-500 bg-rose-50 dark:bg-rose-950/50 text- |
| UI-0437 | 3884 | event-prop | onClick | {() => setBulkPermDirectMode('revoke')} |
| UI-0438 | 3903 | control | button | <button type="button" onClick={() => setBulkPermTargetUsers(userList.map(u => u.id))} className="text-purple-700 dark:text-purple-400 hover:underline text-[11px] font-semibold cursor-pointer" > Chọn tất cả  |
| UI-0439 | 3905 | event-prop | onClick | {() => setBulkPermTargetUsers(userList.map(u => u.id))} |
| UI-0440 | 3911 | control | button | <button type="button" onClick={() => setBulkPermTargetUsers([])} className="text-slate-400 hover:underline text-[11px] cursor-pointer" > Bỏ chọn  |
| UI-0441 | 3913 | event-prop | onClick | {() => setBulkPermTargetUsers([])} |
| UI-0442 | 3931 | control | input | <input type="checkbox" checked={isChecked} onChange={(e) => { if (e.target.checked) { setBulkPermTargetUsers(prev => [...prev, user.id]); } else { setBulkPermTargetUsers(prev => prev.filter(id => id !== user.id)); } }} className="rounded te |
| UI-0443 | 3934 | event-prop | onChange | {(e) => { if (e.target.checked) { setBulkPermTargetUsers(prev => [...prev, user.id]); } else { setBulkPermTargetUsers(prev => prev.filter(id => id !== user.id)); } }} |
| UI-0444 | 3971 | control | button | <button onClick={() => setBulkPermActionModal(null)} className="px-3.5 py-1.5 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-800" > Hủy  |
| UI-0445 | 3972 | event-prop | onClick | {() => setBulkPermActionModal(null)} |
| UI-0446 | 3977 | control | button | <button onClick={handleBulkAssignPermsToUsers} disabled={bulkPermTargetUsers.length === 0} className="px-4 py-1.5 bg-purple-700 hover:bg-purple-800 disabled:opacity-50 text-white rounded-lg text-xs font-semibold cursor-pointer shadow-xs" >  |
| UI-0447 | 3978 | event-prop | onClick | {handleBulkAssignPermsToUsers} |

## components/modeler/DmnTableModule.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0448 | 632 | control | select | <select value={selectedKey} onChange={(e) => { setSelectedKey(e.target.value); setEvaluationResult(null); setActiveEvaluatedRuleId(null); }} className="bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg p |
| UI-0449 | 634 | event-prop | onChange | {(e) => { setSelectedKey(e.target.value); setEvaluationResult(null); setActiveEvaluatedRuleId(null); }} |
| UI-0450 | 654 | control | button | <button key={hp} onClick={() => handleHitPolicyChange(hp)} className={&#96;px-2 py-0.5 rounded text-[10.5px] font-mono font-bold transition-all cursor-pointer ${ currentDecision.hitPolicy === hp ? 'bg-emerald-600 text-white shadow-xs' : 'te |
| UI-0451 | 656 | event-prop | onClick | {() => handleHitPolicyChange(hp)} |
| UI-0452 | 679 | control | button | <button onClick={() => setViewMode('matrix')} className={&#96;flex items-center gap-1.5 px-2.5 py-1 rounded text-xs font-semibold transition-all cursor-pointer ${ viewMode === 'matrix' ? 'bg-white dark:bg-slate-700 text-slate-900 dark:text- |
| UI-0453 | 680 | event-prop | onClick | {() => setViewMode('matrix')} |
| UI-0454 | 690 | control | button | <button onClick={() => setViewMode('visual')} className={&#96;flex items-center gap-1.5 px-2.5 py-1 rounded text-xs font-semibold transition-all cursor-pointer ${ viewMode === 'visual' ? 'bg-white dark:bg-slate-700 text-slate-900 dark:text- |
| UI-0455 | 691 | event-prop | onClick | {() => setViewMode('visual')} |
| UI-0456 | 706 | control | button | <button onClick={() => setShowValidationPanel(!showValidationPanel)} className={&#96;flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-semibold transition-all cursor-pointer border ${ validationReport.hasErrors ? 'bg-rose-50 dar |
| UI-0457 | 707 | event-prop | onClick | {() => setShowValidationPanel(!showValidationPanel)} |
| UI-0458 | 737 | control | button | <button onClick={handleInjectSampleConflict} className="flex items-center gap-1 px-2.5 py-1.5 bg-rose-50 dark:bg-rose-950/40 hover:bg-rose-100 dark:hover:bg-rose-900/50 text-rose-700 dark:text-rose-300 border border-rose-200 dark:border-ros |
| UI-0459 | 738 | event-prop | onClick | {handleInjectSampleConflict} |
| UI-0460 | 747 | control | button | <button onClick={() => { const next = !isSimulationActive; setIsSimulationActive(next); showToast(next ? 'Đã bật chế độ mô phỏng trực tiếp (Simulation ON)' : 'Đã tắt chế độ mô phỏng (Simulation OFF)'); }} className={&#96;flex items-center g |
| UI-0461 | 748 | event-prop | onClick | {() => { const next = !isSimulationActive; setIsSimulationActive(next); showToast(next ? 'Đã bật chế độ mô phỏng trực tiếp (Simulation ON)' : 'Đã tắt chế độ mô phỏng (Simulation OFF)'); }} |
| UI-0462 | 775 | control | button | <button onClick={handleAddRule} className="flex items-center gap-1.5 px-3 py-1.5 bg-emerald-600 hover:bg-emerald-500 active:scale-[0.98] text-white rounded-lg text-xs font-semibold shadow-xs transition-all cursor-pointer" > <Plus className= |
| UI-0463 | 776 | event-prop | onClick | {handleAddRule} |
| UI-0464 | 785 | control | button | <button onClick={handleExportDmn} className="flex items-center gap-1.5 px-3 py-1.5 bg-indigo-600 hover:bg-indigo-500 active:scale-[0.98] text-white rounded-lg text-xs font-semibold shadow-xs transition-all cursor-pointer" title="Xuất bảng q |
| UI-0465 | 786 | event-prop | onClick | {handleExportDmn} |
| UI-0466 | 795 | control | button | <button onClick={handleOpenXml} className="flex items-center gap-1.5 px-3 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 hover:bg-slate-50 text-slate-700 dark:text-slate-200 rounded-lg text-xs font-semibold  |
| UI-0467 | 796 | event-prop | onClick | {handleOpenXml} |
| UI-0468 | 805 | control | button | <button onClick={() => setIsVersionHistoryOpen(!isVersionHistoryOpen)} className={&#96;flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-semibold transition-all cursor-pointer border ${ isVersionHistoryOpen ? 'bg-purple-600 hove |
| UI-0469 | 806 | event-prop | onClick | {() => setIsVersionHistoryOpen(!isVersionHistoryOpen)} |
| UI-0470 | 830 | event-prop | onUpdateDecision | {updateCurrentDecision} |
| UI-0471 | 833 | event-prop | onRunTest | {handleRunEvaluation} |
| UI-0472 | 834 | event-prop | onExportDmn | {handleExportDmn} |
| UI-0473 | 862 | control | input | <input type="text" placeholder="Tìm biểu thức FEEL, kết quả..." value={searchRuleTerm} onChange={(e) => setSearchRuleTerm(e.target.value)} className="pl-7 pr-3 py-1 text-xs bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-s |
| UI-0474 | 866 | event-prop | onChange | {(e) => setSearchRuleTerm(e.target.value)} |
| UI-0475 | 902 | control | button | <button onClick={handleInjectSampleConflict} className="px-2.5 py-1 text-[11px] font-semibold bg-rose-50 dark:bg-rose-950/60 hover:bg-rose-100 dark:hover:bg-rose-900/40 text-rose-700 dark:text-rose-300 border border-rose-200 dark:border-ros |
| UI-0476 | 903 | event-prop | onClick | {handleInjectSampleConflict} |
| UI-0477 | 909 | control | button | <button onClick={() => setShowValidationPanel(false)} className="p-1 text-slate-400 hover:text-slate-600 rounded-lg cursor-pointer" title="Đóng bảng phân tích" > <X className="w-4 h-4" />  |
| UI-0478 | 910 | event-prop | onClick | {() => setShowValidationPanel(false)} |
| UI-0479 | 937 | event-prop | onClick | {() => setHighlightedConflictId(isSelected ? null : conflict.id)} |
| UI-0480 | 968 | control | button | <button onClick={(e) => { e.stopPropagation(); handleRemoveConflictRule(conflict.rule2!.id); }} className="px-2 py-0.5 bg-rose-100 hover:bg-rose-200 text-rose-700 rounded text-[10.5px] font-semibold transition-colors cursor-pointer" > Xóa d |
| UI-0481 | 969 | event-prop | onClick | {(e) => { e.stopPropagation(); handleRemoveConflictRule(conflict.rule2!.id); }} |
| UI-0482 | 1076 | control | button | <button onClick={handleAddInputClause} className="text-[10px] flex items-center gap-0.5 text-sky-600 dark:text-sky-400 hover:underline cursor-pointer" title="Thêm cột điều kiện đầu vào" > <Plus className="w-3 h-3" /> <span>Thêm Cột Input</s |
| UI-0483 | 1077 | event-prop | onClick | {handleAddInputClause} |
| UI-0484 | 1094 | control | button | <button onClick={handleAddOutputClause} className="text-[10px] flex items-center gap-0.5 text-amber-600 dark:text-amber-400 hover:underline cursor-pointer" title="Thêm cột kết quả đầu ra" > <Plus className="w-3 h-3" /> <span>Thêm Cột Output |
| UI-0485 | 1095 | event-prop | onClick | {handleAddOutputClause} |
| UI-0486 | 1206 | control | button | <button type="button" onClick={() => { setShowValidationPanel(true); setHighlightedConflictId(ruleConflicts[0]?.id &#124;&#124; null); }} className="inline-flex items-center justify-center w-5 h-5 rounded-full bg-rose-600 hover:bg-rose-700  |
| UI-0487 | 1208 | event-prop | onClick | {() => { setShowValidationPanel(true); setHighlightedConflictId(ruleConflicts[0]?.id &#124;&#124; null); }} |
| UI-0488 | 1218 | control | button | <button type="button" onClick={() => { setShowValidationPanel(true); setHighlightedConflictId(ruleConflicts[0]?.id &#124;&#124; null); }} className="inline-flex items-center justify-center w-5 h-5 rounded-full bg-amber-500 hover:bg-amber-60 |
| UI-0489 | 1220 | event-prop | onClick | {() => { setShowValidationPanel(true); setHighlightedConflictId(ruleConflicts[0]?.id &#124;&#124; null); }} |
| UI-0490 | 1252 | control | input | <input type="text" value={cellVal} onChange={(e) => handleCellChange(rule.id, input.id, true, e.target.value)} className={&#96;w-full px-2 py-1 rounded bg-transparent text-xs font-mono focus:bg-white dark:focus:bg-slate-800 focus:outline-no |
| UI-0491 | 1255 | event-prop | onChange | {(e) => handleCellChange(rule.id, input.id, true, e.target.value)} |
| UI-0492 | 1291 | control | input | <input type="text" value={typeof cellVal === 'number' ? cellVal.toLocaleString() : String(cellVal)} onChange={(e) => { const raw = e.target.value.replace(/,/g, ''); const parsed = output.typeRef === 'number' && !isNaN(Number(raw)) ? Number( |
| UI-0493 | 1294 | event-prop | onChange | {(e) => { const raw = e.target.value.replace(/,/g, ''); const parsed = output.typeRef === 'number' && !isNaN(Number(raw)) ? Number(raw) : raw; handleCellChange(rule.id, output.id, false, parsed); }} |
| UI-0494 | 1315 | control | input | <input type="text" value={rule.annotation &#124;&#124; ''} onChange={(e) => { const updatedRules = currentDecision.rules.map(r => r.id === rule.id ? { ...r, annotation: e.target.value } : r); updateCurrentDecision({ ...currentDecision, rule |
| UI-0495 | 1318 | event-prop | onChange | {(e) => { const updatedRules = currentDecision.rules.map(r => r.id === rule.id ? { ...r, annotation: e.target.value } : r); updateCurrentDecision({ ...currentDecision, rules: updatedRules }); }} |
| UI-0496 | 1330 | control | button | <button onClick={() => handleDuplicateRule(rule)} className="p-1 text-slate-400 hover:text-slate-600 rounded cursor-pointer" title="Nhân bản quy tắc này" > <Copy className="w-3.5 h-3.5" />  |
| UI-0497 | 1331 | event-prop | onClick | {() => handleDuplicateRule(rule)} |
| UI-0498 | 1337 | control | button | <button onClick={() => handleDeleteRule(rule.id)} className="p-1 text-slate-400 hover:text-rose-600 rounded cursor-pointer" title="Xóa quy tắc này" > <Trash2 className="w-3.5 h-3.5" />  |
| UI-0499 | 1338 | event-prop | onClick | {() => handleDeleteRule(rule.id)} |
| UI-0500 | 1398 | control | button | <button key={preset.id} type="button" onClick={() => { setTestInputs(preset.values); showToast(&#96;Đã nạp kịch bản: ${preset.name}&#96;); }} className="p-1.5 text-left bg-white dark:bg-slate-800 hover:bg-emerald-50 dark:hover:bg-emerald-95 |
| UI-0501 | 1401 | event-prop | onClick | {() => { setTestInputs(preset.values); showToast(&#96;Đã nạp kịch bản: ${preset.name}&#96;); }} |
| UI-0502 | 1434 | control | select | <select value={testInputs[input.id] &#124;&#124; input.allowedValues[0]} onChange={(e) => setTestInputs(prev => ({ ...prev, [input.id]: e.target.value }))} className="w-full px-2.5 py-1.5 bg-slate-50 dark:bg-slate-800 border border-slate-20 |
| UI-0503 | 1436 | event-prop | onChange | {(e) => setTestInputs(prev => ({ ...prev, [input.id]: e.target.value }))} |
| UI-0504 | 1445 | control | button | <button type="button" onClick={() => setTestInputs(prev => ({ ...prev, [input.id]: true }))} className={&#96;flex-1 py-1 rounded text-xs font-bold border cursor-pointer ${ testInputs[input.id] === true ? 'bg-emerald-600 text-white border-em |
| UI-0505 | 1447 | event-prop | onClick | {() => setTestInputs(prev => ({ ...prev, [input.id]: true }))} |
| UI-0506 | 1454 | control | button | <button type="button" onClick={() => setTestInputs(prev => ({ ...prev, [input.id]: false }))} className={&#96;flex-1 py-1 rounded text-xs font-bold border cursor-pointer ${ testInputs[input.id] === false ? 'bg-rose-600 text-white border-ros |
| UI-0507 | 1456 | event-prop | onClick | {() => setTestInputs(prev => ({ ...prev, [input.id]: false }))} |
| UI-0508 | 1465 | control | input | <input type="number" value={testInputs[input.id] ?? ''} onChange={(e) => setTestInputs(prev => ({ ...prev, [input.id]: parseFloat(e.target.value) &#124;&#124; 0 }))} className="w-full px-2.5 py-1.5 bg-slate-50 dark:bg-slate-800 border borde |
| UI-0509 | 1468 | event-prop | onChange | {(e) => setTestInputs(prev => ({ ...prev, [input.id]: parseFloat(e.target.value) &#124;&#124; 0 }))} |
| UI-0510 | 1481 | control | button | <button type="button" onClick={handleRunEvaluation} className="px-2 py-0.5 bg-emerald-600 text-white text-[10px] font-bold rounded hover:bg-emerald-500 cursor-pointer" title="Tính toán lại ngay lập tức" > Tính lại  |
| UI-0511 | 1483 | event-prop | onClick | {handleRunEvaluation} |
| UI-0512 | 1555 | event-prop | onClose | {() => setIsVersionHistoryOpen(false)} |
| UI-0513 | 1558 | event-prop | onSaveSnapshot | {handleSaveSnapshot} |
| UI-0514 | 1559 | event-prop | onRevertToVersion | {handleRevertVersion} |
| UI-0515 | 1560 | event-prop | onCompareVersions | {(base, target) => setCompareModalState({ isOpen: true, base, target })} |
| UI-0516 | 1561 | event-prop | onInspectVersion | {(v) => setInspectModalVersion(v)} |
| UI-0517 | 1582 | control | button | <button onClick={() => setShowXmlModal(false)} className="text-slate-400 hover:text-slate-600 p-1 rounded-lg" > ✕  |
| UI-0518 | 1583 | event-prop | onClick | {() => setShowXmlModal(false)} |
| UI-0519 | 1590 | control | textarea | <textarea value={xmlContent} onChange={(e) => setXmlContent(e.target.value)} className="w-full h-96 bg-transparent text-amber-400 font-mono text-xs focus:outline-none resize-none leading-relaxed" spellCheck={false} /> </div> <div className= |
| UI-0520 | 1592 | event-prop | onChange | {(e) => setXmlContent(e.target.value)} |
| UI-0521 | 1600 | control | button | <button onClick={handleCopyXml} className="px-3.5 py-1.5 border border-slate-300 dark:border-slate-600 rounded-lg text-xs font-semibold text-slate-700 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 flex items-center gap-1.5  |
| UI-0522 | 1601 | event-prop | onClick | {handleCopyXml} |
| UI-0523 | 1608 | control | button | <button onClick={() => { const blob = new Blob([xmlContent], { type: 'application/xml' }); const url = URL.createObjectURL(blob); const a = document.createElement('a'); a.href = url; a.download = &#96;${currentDecision.key}.dmn&#96;; a.clic |
| UI-0524 | 1609 | event-prop | onClick | {() => { const blob = new Blob([xmlContent], { type: 'application/xml' }); const url = URL.createObjectURL(blob); const a = document.createElement('a'); a.href = url; a.download = &#96;${currentDecision.key}.dmn&#96;; a.click(); URL.revokeO |
| UI-0525 | 1625 | control | button | <button onClick={() => { setShowXmlModal(false); showToast('Đã đóng cửa sổ DMN 1.3 XML.'); }} className="px-4 py-1.5 bg-slate-200 dark:bg-slate-700 hover:bg-slate-300 dark:hover:bg-slate-600 text-slate-800 dark:text-slate-200 rounded-lg tex |
| UI-0526 | 1626 | event-prop | onClick | {() => { setShowXmlModal(false); showToast('Đã đóng cửa sổ DMN 1.3 XML.'); }} |
| UI-0527 | 1644 | event-prop | onClose | {() => setCompareModalState(null)} |
| UI-0528 | 1648 | event-prop | onRevertToVersion | {handleRevertVersion} |
| UI-0529 | 1656 | event-prop | onClose | {() => setInspectModalVersion(null)} |
| UI-0530 | 1657 | event-prop | onRevert | {handleRevertVersion} |

## components/modeler/DmnVersionCompareModal.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0531 | 94 | control | button | <button onClick={onClose} className="p-1.5 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 rounded-lg hover:bg-slate-200/60 dark:hover:bg-slate-800 cursor-pointer transition-colors" > <X className="w-4 h-4" />  |
| UI-0532 | 95 | event-prop | onClick | {onClose} |
| UI-0533 | 108 | control | select | <select value={selectedBaseId} onChange={(e) => setSelectedBaseId(e.target.value)} className="px-2.5 py-1 text-xs font-bold bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg text-slate-800 dark:text-slat |
| UI-0534 | 110 | event-prop | onChange | {(e) => setSelectedBaseId(e.target.value)} |
| UI-0535 | 125 | control | select | <select value={selectedTargetId} onChange={(e) => setSelectedTargetId(e.target.value)} className="px-2.5 py-1 text-xs font-bold bg-emerald-50/50 dark:bg-emerald-950/30 border border-emerald-300 dark:border-emerald-800 rounded-lg text-emeral |
| UI-0536 | 127 | event-prop | onChange | {(e) => setSelectedTargetId(e.target.value)} |
| UI-0537 | 348 | control | button | <button onClick={onClose} className="px-4 py-2 border border-slate-200 dark:border-slate-700 hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-700 dark:text-slate-300 rounded-xl text-xs font-semibold cursor-pointer transition-colors" >  |
| UI-0538 | 349 | event-prop | onClick | {onClose} |
| UI-0539 | 356 | control | button | <button onClick={() => { onRevertToVersion(activeBase); onClose(); }} className="px-4 py-2 bg-purple-600 hover:bg-purple-500 text-white rounded-xl text-xs font-bold flex items-center gap-1.5 shadow-xs cursor-pointer transition-all active:sc |
| UI-0540 | 357 | event-prop | onClick | {() => { onRevertToVersion(activeBase); onClose(); }} |

## components/modeler/DmnVersionHistorySidebar.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0541 | 108 | control | button | <button onClick={onClose} className="p-1 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 rounded-lg hover:bg-slate-200/50 dark:hover:bg-slate-800 cursor-pointer transition-colors" title="Đóng bảng lịch sử" > <X className="w-4  |
| UI-0542 | 109 | event-prop | onClick | {onClose} |
| UI-0543 | 120 | control | button | <button onClick={handleOpenCreateSnapshot} className="w-full py-2 px-3 bg-purple-600 hover:bg-purple-500 active:scale-[0.98] text-white font-bold rounded-xl text-xs shadow-xs transition-all flex items-center justify-center gap-1.5 cursor-po |
| UI-0544 | 121 | event-prop | onClick | {handleOpenCreateSnapshot} |
| UI-0545 | 128 | control | form | <form onSubmit={handleSubmitSnapshot} className="p-3 bg-white dark:bg-slate-800 rounded-xl border border-purple-300 dark:border-purple-800 shadow-sm space-y-2.5 animate-in fade-in duration-150"> <div className="flex items-center justify-bet |
| UI-0546 | 128 | event-prop | onSubmit | {handleSubmitSnapshot} |
| UI-0547 | 133 | control | button | <button type="button" onClick={() => setIsCreatingSnapshot(false)} className="text-slate-400 hover:text-slate-600 text-[11px]" > Hủy  |
| UI-0548 | 135 | event-prop | onClick | {() => setIsCreatingSnapshot(false)} |
| UI-0549 | 145 | control | input | <input type="text" value={newVersionTag} onChange={(e) => setNewVersionTag(e.target.value)} placeholder="v2.1" className="w-full px-2 py-1 bg-slate-50 dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-xs font-m |
| UI-0550 | 148 | event-prop | onChange | {(e) => setNewVersionTag(e.target.value)} |
| UI-0551 | 156 | control | input | <input type="text" value={newAuthor} onChange={(e) => setNewAuthor(e.target.value)} className="w-full px-2 py-1 bg-slate-50 dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-xs focus:outline-none focus:ring-1 f |
| UI-0552 | 159 | event-prop | onChange | {(e) => setNewAuthor(e.target.value)} |
| UI-0553 | 168 | control | textarea | <textarea value={newSummary} onChange={(e) => setNewSummary(e.target.value)} placeholder="Mô tả lý do thay đổi quy tắc, cập nhật điều kiện FEEL..." className="w-full px-2 py-1 bg-slate-50 dark:bg-slate-900 border border-slate-200 dark:borde |
| UI-0554 | 170 | event-prop | onChange | {(e) => setNewSummary(e.target.value)} |
| UI-0555 | 177 | control | button | <button type="submit" className="w-full py-1.5 bg-purple-600 hover:bg-purple-500 text-white font-bold rounded-lg text-xs shadow-xs cursor-pointer transition-colors" > Lưu Snapshot Ngay  |
| UI-0556 | 188 | control | input | <input type="text" value={searchTerm} onChange={(e) => setSearchTerm(e.target.value)} placeholder="Tìm theo thẻ phiên bản, người sửa, ghi chú..." className="w-full pl-7 pr-3 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:bor |
| UI-0557 | 191 | event-prop | onChange | {(e) => setSearchTerm(e.target.value)} |
| UI-0558 | 277 | control | button | <button onClick={() => onInspectVersion(ver)} className="px-2 py-1 text-[10.5px] font-semibold text-slate-600 dark:text-slate-400 hover:text-slate-900 dark:hover:text-slate-200 hover:bg-slate-100 dark:hover:bg-slate-800 rounded-lg transitio |
| UI-0559 | 278 | event-prop | onClick | {() => onInspectVersion(ver)} |
| UI-0560 | 287 | control | button | <button onClick={() => onCompareVersions(ver, currentVersion)} className="px-2 py-1 text-[10.5px] font-semibold text-purple-700 dark:text-purple-300 bg-purple-50 dark:bg-purple-950/60 hover:bg-purple-100 border border-purple-200 dark:border |
| UI-0561 | 288 | event-prop | onClick | {() => onCompareVersions(ver, currentVersion)} |
| UI-0562 | 298 | control | button | <button onClick={() => setConfirmRevertSnapshot(ver)} className="px-2 py-1 text-[10.5px] font-semibold text-amber-800 dark:text-amber-200 bg-amber-50 dark:bg-amber-950/60 hover:bg-amber-100 border border-amber-300 dark:border-amber-800 roun |
| UI-0563 | 299 | event-prop | onClick | {() => setConfirmRevertSnapshot(ver)} |
| UI-0564 | 327 | control | button | <button onClick={() => setConfirmRevertSnapshot(null)} className="px-2.5 py-1 text-[11px] font-semibold text-slate-600 hover:bg-amber-100 dark:hover:bg-amber-900/40 rounded-lg cursor-pointer" > Hủy  |
| UI-0565 | 328 | event-prop | onClick | {() => setConfirmRevertSnapshot(null)} |
| UI-0566 | 333 | control | button | <button onClick={() => { onRevertToVersion(confirmRevertSnapshot); setConfirmRevertSnapshot(null); }} className="px-3 py-1 text-[11px] font-bold bg-amber-600 hover:bg-amber-500 text-white rounded-lg shadow-xs cursor-pointer" > Xác Nhận Khôi |
| UI-0567 | 334 | event-prop | onClick | {() => { onRevertToVersion(confirmRevertSnapshot); setConfirmRevertSnapshot(null); }} |

## components/modeler/DmnVersionInspectModal.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0568 | 54 | control | button | <button onClick={onClose} className="p-1.5 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 rounded-lg hover:bg-slate-200/60 dark:hover:bg-slate-800 cursor-pointer transition-colors" > <X className="w-4 h-4" />  |
| UI-0569 | 55 | event-prop | onClick | {onClose} |
| UI-0570 | 136 | control | button | <button onClick={onClose} className="px-4 py-2 border border-slate-200 dark:border-slate-700 text-slate-700 dark:text-slate-300 rounded-xl text-xs font-semibold hover:bg-slate-100 dark:hover:bg-slate-800 cursor-pointer" > Đóng  |
| UI-0571 | 137 | event-prop | onClick | {onClose} |
| UI-0572 | 143 | control | button | <button onClick={() => { onRevert(version); onClose(); }} className="px-4 py-2 bg-purple-600 hover:bg-purple-500 text-white rounded-xl text-xs font-bold flex items-center gap-1.5 shadow-xs cursor-pointer" > <RotateCcw className="w-3.5 h-3.5 |
| UI-0573 | 144 | event-prop | onClick | {() => { onRevert(version); onClose(); }} |

## components/modeler/DmnVisualLogicEditor.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0574 | 240 | control | input | <input type="text" placeholder="Lọc hàng quy tắc logic..." value={searchTerm} onChange={(e) => setSearchTerm(e.target.value)} className="pl-7 pr-3 py-1.5 text-xs bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 ro |
| UI-0575 | 244 | event-prop | onChange | {(e) => setSearchTerm(e.target.value)} |
| UI-0576 | 251 | control | button | <button onClick={onRunTest} className="flex items-center gap-1.5 px-3.5 py-1.5 bg-amber-600 hover:bg-amber-500 active:scale-[0.98] text-white rounded-xl text-xs font-semibold shadow-xs transition-all cursor-pointer" title="Kiểm thử dữ liệu  |
| UI-0577 | 252 | event-prop | onClick | {onRunTest} |
| UI-0578 | 262 | control | button | <button onClick={onExportDmn} className="flex items-center gap-1.5 px-3.5 py-1.5 bg-indigo-600 hover:bg-indigo-500 active:scale-[0.98] text-white rounded-xl text-xs font-semibold shadow-xs transition-all cursor-pointer" title="Xuất bảng quy |
| UI-0579 | 263 | event-prop | onClick | {onExportDmn} |
| UI-0580 | 273 | control | button | <button onClick={handleOpenCreateRuleModal} className="flex items-center gap-1.5 px-3.5 py-1.5 bg-emerald-600 hover:bg-emerald-500 active:scale-[0.98] text-white rounded-xl text-xs font-semibold shadow-xs transition-all cursor-pointer" > <P |
| UI-0581 | 274 | event-prop | onClick | {handleOpenCreateRuleModal} |
| UI-0582 | 305 | event-prop | onClick | {() => setSelectedInputFilter(isFilterActive ? null : input.id)} |
| UI-0583 | 363 | control | button | <button onClick={() => { setSelectedInputFilter(null); setSelectedOutputFilter(null); }} className="text-xs text-rose-600 hover:underline flex items-center gap-1 cursor-pointer" > <X className="w-3.5 h-3.5" /> <span>Xóa bộ lọc biến</span>  |
| UI-0584 | 364 | event-prop | onClick | {() => { setSelectedInputFilter(null); setSelectedOutputFilter(null); }} |
| UI-0585 | 435 | control | button | <button onClick={() => handleOpenEditRuleModal(rule)} className="flex items-center gap-1 px-2 py-1 bg-white dark:bg-slate-800 hover:bg-slate-100 dark:hover:bg-slate-700 border border-slate-200 dark:border-slate-700 text-slate-700 dark:text- |
| UI-0586 | 436 | event-prop | onClick | {() => handleOpenEditRuleModal(rule)} |
| UI-0587 | 443 | control | button | <button onClick={() => handleDuplicateRule(rule)} className="p-1 text-slate-400 hover:text-slate-600 rounded-lg cursor-pointer" title="Nhân bản quy tắc" > <Copy className="w-3.5 h-3.5" />  |
| UI-0588 | 444 | event-prop | onClick | {() => handleDuplicateRule(rule)} |
| UI-0589 | 450 | control | button | <button onClick={() => handleDeleteRule(rule.id)} className="p-1 text-slate-400 hover:text-rose-600 rounded-lg cursor-pointer" title="Xóa quy tắc này" > <Trash2 className="w-3.5 h-3.5" />  |
| UI-0590 | 451 | event-prop | onClick | {() => handleDeleteRule(rule.id)} |
| UI-0591 | 504 | event-prop | onClick | {() => handleOpenEditRuleModal(rule)} |
| UI-0592 | 542 | event-prop | onClick | {() => handleOpenEditRuleModal(rule)} |
| UI-0593 | 592 | event-prop | onClick | {() => setSelectedOutputFilter(isFilterActive ? null : output.id)} |
| UI-0594 | 646 | control | button | <button onClick={() => { setSelectedRuleForEdit(null); setIsCreatingNewRule(false); }} className="text-slate-400 hover:text-slate-600 p-1 rounded-lg" > ✕  |
| UI-0595 | 647 | event-prop | onClick | {() => { setSelectedRuleForEdit(null); setIsCreatingNewRule(false); }} |
| UI-0596 | 661 | control | input | <input type="text" value={editingAnnotation} onChange={(e) => setEditingAnnotation(e.target.value)} placeholder="VD: Khách hàng đạt chuẩn phê duyệt tự động cấp hạn mức cao..." className="w-full px-3 py-1.5 bg-slate-50 dark:bg-slate-800 bord |
| UI-0597 | 664 | event-prop | onChange | {(e) => setEditingAnnotation(e.target.value)} |
| UI-0598 | 689 | control | select | <select value={currentEntry.op} onChange={(e) => { setEditingRuleInputs(prev => ({ ...prev, [inp.id]: { ...currentEntry, op: e.target.value } })); }} className="px-2 py-1 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-sla |
| UI-0599 | 691 | event-prop | onChange | {(e) => { setEditingRuleInputs(prev => ({ ...prev, [inp.id]: { ...currentEntry, op: e.target.value } })); }} |
| UI-0600 | 711 | control | select | <select value={currentEntry.val &#124;&#124; inp.allowedValues[0]} onChange={(e) => { setEditingRuleInputs(prev => ({ ...prev, [inp.id]: { ...currentEntry, val: e.target.value } })); }} className="flex-1 px-2 py-1 bg-slate-50 dark:bg-slate- |
| UI-0601 | 713 | event-prop | onChange | {(e) => { setEditingRuleInputs(prev => ({ ...prev, [inp.id]: { ...currentEntry, val: e.target.value } })); }} |
| UI-0602 | 726 | control | input | <input type={inp.typeRef === 'number' ? 'number' : 'text'} value={currentEntry.val} onChange={(e) => { setEditingRuleInputs(prev => ({ ...prev, [inp.id]: { ...currentEntry, val: e.target.value } })); }} placeholder="Nhập giá trị so sánh..." |
| UI-0603 | 729 | event-prop | onChange | {(e) => { setEditingRuleInputs(prev => ({ ...prev, [inp.id]: { ...currentEntry, val: e.target.value } })); }} |
| UI-0604 | 765 | control | select | <select value={currentVal &#124;&#124; out.allowedValues[0]} onChange={(e) => setEditingRuleOutputs(prev => ({ ...prev, [out.id]: e.target.value }))} className="flex-1 px-2 py-1 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:bor |
| UI-0605 | 767 | event-prop | onChange | {(e) => setEditingRuleOutputs(prev => ({ ...prev, [out.id]: e.target.value }))} |
| UI-0606 | 776 | control | button | <button type="button" onClick={() => setEditingRuleOutputs(prev => ({ ...prev, [out.id]: true }))} className={&#96;flex-1 py-1 rounded text-xs font-bold border cursor-pointer ${ currentVal === true ? 'bg-emerald-600 text-white border-emeral |
| UI-0607 | 778 | event-prop | onClick | {() => setEditingRuleOutputs(prev => ({ ...prev, [out.id]: true }))} |
| UI-0608 | 785 | control | button | <button type="button" onClick={() => setEditingRuleOutputs(prev => ({ ...prev, [out.id]: false }))} className={&#96;flex-1 py-1 rounded text-xs font-bold border cursor-pointer ${ currentVal === false ? 'bg-rose-600 text-white border-rose-60 |
| UI-0609 | 787 | event-prop | onClick | {() => setEditingRuleOutputs(prev => ({ ...prev, [out.id]: false }))} |
| UI-0610 | 796 | control | input | <input type={out.typeRef === 'number' ? 'number' : 'text'} value={currentVal ?? ''} onChange={(e) => { const val = out.typeRef === 'number' ? parseFloat(e.target.value) &#124;&#124; 0 : e.target.value; setEditingRuleOutputs(prev => ({ ...pr |
| UI-0611 | 799 | event-prop | onChange | {(e) => { const val = out.typeRef === 'number' ? parseFloat(e.target.value) &#124;&#124; 0 : e.target.value; setEditingRuleOutputs(prev => ({ ...prev, [out.id]: val })); }} |
| UI-0612 | 819 | control | button | <button onClick={() => { setSelectedRuleForEdit(null); setIsCreatingNewRule(false); }} className="px-3.5 py-1.5 border border-slate-300 dark:border-slate-700 rounded-xl text-xs font-semibold text-slate-600 cursor-pointer" > Hủy  |
| UI-0613 | 820 | event-prop | onClick | {() => { setSelectedRuleForEdit(null); setIsCreatingNewRule(false); }} |
| UI-0614 | 825 | control | button | <button onClick={handleSaveModalRule} className="px-4 py-1.5 bg-emerald-600 hover:bg-emerald-500 text-white rounded-xl text-xs font-semibold shadow-xs transition-all cursor-pointer" > {isCreatingNewRule ? 'Thêm Quy Tắc' : 'Cập Nhật Quy Tắc' |
| UI-0615 | 826 | event-prop | onClick | {handleSaveModalRule} |

## components/modeler/ModelerModule.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0616 | 380 | control | button | <button onClick={() => setActiveModelerMode('bpmn')} className={&#96;flex items-center gap-1.5 px-3 py-1 rounded-lg transition-all cursor-pointer ${ activeModelerMode === 'bpmn' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-1 |
| UI-0617 | 381 | event-prop | onClick | {() => setActiveModelerMode('bpmn')} |
| UI-0618 | 392 | control | button | <button onClick={() => setActiveModelerMode('dmn')} className={&#96;flex items-center gap-1.5 px-3 py-1 rounded-lg transition-all cursor-pointer ${ activeModelerMode === 'dmn' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 |
| UI-0619 | 393 | event-prop | onClick | {() => setActiveModelerMode('dmn')} |
| UI-0620 | 410 | control | select | <select value={currentProcess.version} onChange={() => {}} className="bg-slate-50 dark:bg-slate-800/80 border border-slate-200 dark:border-slate-700/80 rounded-lg px-2.5 py-1 font-semibold text-slate-800 dark:text-slate-200 text-xs shadow-2 |
| UI-0621 | 412 | event-prop | onChange | {() => {}} |
| UI-0622 | 435 | control | button | <button onClick={() => setZoomLevel(prev => Math.max(0.6, prev - 0.1))} className="px-2 py-1 text-slate-600 dark:text-slate-300 hover:text-slate-900 cursor-pointer" title="Thu nhỏ" > -  |
| UI-0623 | 436 | event-prop | onClick | {() => setZoomLevel(prev => Math.max(0.6, prev - 0.1))} |
| UI-0624 | 445 | control | button | <button onClick={() => setZoomLevel(prev => Math.min(1.5, prev + 0.1))} className="px-2 py-1 text-slate-600 dark:text-slate-300 hover:text-slate-900 cursor-pointer" title="Phóng to" > +  |
| UI-0625 | 446 | event-prop | onClick | {() => setZoomLevel(prev => Math.min(1.5, prev + 0.1))} |
| UI-0626 | 452 | control | button | <button onClick={() => { setZoomLevel(1); setCanvasOffset({ x: 0, y: 0 }); }} className="px-1.5 py-1 text-[10px] text-slate-400 hover:text-slate-700 cursor-pointer border-l border-slate-200 dark:border-slate-700" title="Khôi phục 100%" > Re |
| UI-0627 | 453 | event-prop | onClick | {() => { setZoomLevel(1); setCanvasOffset({ x: 0, y: 0 }); }} |
| UI-0628 | 462 | control | button | <button onClick={() => setShowDeployModal(true)} className="flex items-center gap-1.5 px-3.5 py-1.5 bg-emerald-600 hover:bg-emerald-500 active:scale-[0.98] text-white rounded-lg text-xs font-semibold shadow-xs transition-all cursor-pointer" |
| UI-0629 | 463 | event-prop | onClick | {() => setShowDeployModal(true)} |
| UI-0630 | 471 | control | button | <button onClick={handleOpenXmlModal} className="flex items-center gap-1.5 px-3 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 hover:bg-slate-50 text-slate-700 dark:text-slate-200 rounded-lg text-xs font-semi |
| UI-0631 | 472 | event-prop | onClick | {handleOpenXmlModal} |
| UI-0632 | 487 | event-prop | onDeployDecision | {(dec) => { showToast(&#96;Đã triển khai bảng quyết định DMN [${dec.name}] thành công!&#96;); }} |
| UI-0633 | 496 | event-prop | onAddNode | {handleAddPaletteNode} |
| UI-0634 | 498 | event-prop | onToggleExpand | {() => setIsPaletteExpanded(!isPaletteExpanded)} |
| UI-0635 | 504 | event-prop | onMouseMove | {handleMouseMoveCanvas} |
| UI-0636 | 505 | event-prop | onMouseUp | {handleMouseUpCanvas} |
| UI-0637 | 506 | event-prop | onMouseDown | {handleCanvasMouseDown} |
| UI-0638 | 519 | control | button | <button onClick={() => { setActiveTool('select'); setConnectingSourceId(null); }} className={&#96;p-1.5 rounded-lg transition-colors cursor-pointer ${ activeTool === 'select' ? 'bg-slate-100 dark:bg-slate-700 text-emerald-600 font-bold' : ' |
| UI-0639 | 520 | event-prop | onClick | {() => { setActiveTool('select'); setConnectingSourceId(null); }} |
| UI-0640 | 529 | control | button | <button onClick={() => { setActiveTool('pan'); setConnectingSourceId(null); }} className={&#96;p-1.5 rounded-lg transition-colors cursor-pointer ${ activeTool === 'pan' ? 'bg-slate-100 dark:bg-slate-700 text-emerald-600 font-bold' : 'text-s |
| UI-0641 | 530 | event-prop | onClick | {() => { setActiveTool('pan'); setConnectingSourceId(null); }} |
| UI-0642 | 539 | control | button | <button onClick={() => { setActiveTool('connect'); showToast('Chọn node nguồn, sau đó nhấp node đích để nối Sequence Flow'); }} className={&#96;p-1.5 rounded-lg transition-colors cursor-pointer ${ activeTool === 'connect' ? 'bg-emerald-50 d |
| UI-0643 | 540 | event-prop | onClick | {() => { setActiveTool('connect'); showToast('Chọn node nguồn, sau đó nhấp node đích để nối Sequence Flow'); }} |
| UI-0644 | 552 | control | button | <button onClick={() => { setTokenSimActive(!tokenSimActive); if (!tokenSimActive) setSimPlaying(true); }} className={&#96;flex items-center gap-1.5 px-2.5 py-1 rounded-lg transition-all cursor-pointer text-xs font-semibold ${ tokenSimActive |
| UI-0645 | 553 | event-prop | onClick | {() => { setTokenSimActive(!tokenSimActive); if (!tokenSimActive) setSimPlaying(true); }} |
| UI-0646 | 570 | control | button | <button onClick={() => setSimPlaying(!simPlaying)} className="p-1 hover:bg-slate-100 dark:hover:bg-slate-700 rounded text-slate-700 dark:text-slate-300" title={simPlaying ? 'Tạm dừng' : 'Chạy tiếp'} > {simPlaying ? <Pause className="w-3.5 h |
| UI-0647 | 571 | event-prop | onClick | {() => setSimPlaying(!simPlaying)} |
| UI-0648 | 577 | control | button | <button onClick={() => setSimStepIndex(0)} className="p-1 hover:bg-slate-100 dark:hover:bg-slate-700 rounded text-slate-700 dark:text-slate-300" title="Khởi động lại mô phỏng" > <RotateCcw className="w-3.5 h-3.5" />  |
| UI-0649 | 578 | event-prop | onClick | {() => setSimStepIndex(0)} |
| UI-0650 | 590 | control | button | <button onClick={() => setShowMinimap(!showMinimap)} className="px-2.5 py-1 text-xs font-medium bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg shadow-sm text-slate-600 dark:text-slate-300 hover:bg-slate- |
| UI-0651 | 591 | event-prop | onClick | {() => setShowMinimap(!showMinimap)} |
| UI-0652 | 810 | event-prop | onMouseDown | {(e) => handleMouseDownNode(e, node)} |
| UI-0653 | 1001 | event-prop | onClick | {(e) => { e.stopPropagation(); handleAppendNode(node, 'userTask', 'BƯỚC TIẾP THEO'); }} |
| UI-0654 | 1012 | event-prop | onClick | {(e) => { e.stopPropagation(); handleAppendNode(node, 'exclusiveGateway', 'X'); }} |
| UI-0655 | 1023 | event-prop | onClick | {(e) => { e.stopPropagation(); handleAppendNode(node, 'serviceTask', 'ZEEBE WORKER'); }} |
| UI-0656 | 1034 | event-prop | onClick | {(e) => { e.stopPropagation(); handleDeleteNode(node.id); }} |
| UI-0657 | 1055 | event-prop | onUpdateNode | {(updated) => { const updatedNodes = currentProcess.nodes.map(n => n.id === updated.id ? updated : n); onUpdateProcess({ ...currentProcess, nodes: updatedNodes }); }} |
| UI-0658 | 1059 | event-prop | onUpdateProcess | {onUpdateProcess} |
| UI-0659 | 1061 | event-prop | onOpenDmn | {(key) => { setActiveDmnKey(key); setActiveModelerMode('dmn'); showToast(&#96;Đã chuyển sang bảng quyết định DMN: [${key}]&#96;); }} |
| UI-0660 | 1086 | control | button | <button onClick={() => setShowXmlModal(false)} className="text-slate-400 hover:text-slate-600 p-1 rounded-lg" > ✕  |
| UI-0661 | 1087 | event-prop | onClick | {() => setShowXmlModal(false)} |
| UI-0662 | 1094 | control | textarea | <textarea value={xmlContent} onChange={(e) => setXmlContent(e.target.value)} className="w-full h-96 bg-transparent text-emerald-400 font-mono text-xs focus:outline-none resize-none leading-relaxed" spellCheck={false} /> </div> <div classNam |
| UI-0663 | 1096 | event-prop | onChange | {(e) => setXmlContent(e.target.value)} |
| UI-0664 | 1104 | control | button | <button onClick={() => { const blob = new Blob([xmlContent], { type: 'application/xml' }); const url = URL.createObjectURL(blob); const a = document.createElement('a'); a.href = url; a.download = &#96;${currentProcess.key}_camunda8_v${curre |
| UI-0665 | 1105 | event-prop | onClick | {() => { const blob = new Blob([xmlContent], { type: 'application/xml' }); const url = URL.createObjectURL(blob); const a = document.createElement('a'); a.href = url; a.download = &#96;${currentProcess.key}_camunda8_v${currentProcess.versio |
| UI-0666 | 1117 | control | button | <button onClick={() => { onUpdateProcess({ ...currentProcess, xml: xmlContent }); setShowXmlModal(false); showToast('Đã lưu cấu hình BPMN 2.0 XML.'); }} className="px-4 py-1.5 bg-emerald-600 text-white rounded-lg text-xs font-semibold hover |
| UI-0667 | 1118 | event-prop | onClick | {() => { onUpdateProcess({ ...currentProcess, xml: xmlContent }); setShowXmlModal(false); showToast('Đã lưu cấu hình BPMN 2.0 XML.'); }} |
| UI-0668 | 1142 | control | button | <button onClick={() => setShowDeployModal(false)} className="text-slate-400 hover:text-slate-600">✕ |
| UI-0669 | 1142 | event-prop | onClick | {() => setShowDeployModal(false)} |
| UI-0670 | 1151 | control | button | <button key={engine} onClick={() => setTargetEngine(engine)} className={&#96;p-3 rounded-xl border text-left cursor-pointer transition-all ${ targetEngine === engine ? 'border-emerald-600 bg-emerald-50 dark:bg-emerald-950/40 text-emerald-80 |
| UI-0671 | 1153 | event-prop | onClick | {() => setTargetEngine(engine)} |
| UI-0672 | 1189 | control | button | <button onClick={() => setShowDeployModal(false)} className="px-3.5 py-1.5 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold text-slate-600" > Hủy  |
| UI-0673 | 1190 | event-prop | onClick | {() => setShowDeployModal(false)} |
| UI-0674 | 1195 | control | button | <button onClick={() => { onDeployToEngine(targetEngine); setShowDeployModal(false); showToast(&#96;Đã triển khai thành công quy trình lên ${targetEngine} Engine!&#96;); }} className="px-4 py-1.5 bg-emerald-600 hover:bg-emerald-500 text-whit |
| UI-0675 | 1196 | event-prop | onClick | {() => { onDeployToEngine(targetEngine); setShowDeployModal(false); showToast(&#96;Đã triển khai thành công quy trình lên ${targetEngine} Engine!&#96;); }} |

## components/modeler/ModelerPalette.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0676 | 298 | control | button | <button onClick={onToggleExpand} className="p-1 rounded hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-500 hover:text-slate-800 dark:hover:text-slate-200 cursor-pointer ml-auto" title={isExpanded ? 'Thu gọn Palette' : 'Mở rộng bảng đ |
| UI-0677 | 299 | event-prop | onClick | {onToggleExpand} |
| UI-0678 | 312 | control | input | <input type="text" placeholder="Tìm kiếm tác vụ, tác nhân..." value={searchTerm} onChange={(e) => setSearchTerm(e.target.value)} className="w-full pl-7 pr-2 py-1 text-xs bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slat |
| UI-0679 | 316 | event-prop | onChange | {(e) => setSearchTerm(e.target.value)} |
| UI-0680 | 330 | control | button | <button onClick={() => toggleCategory(cat)} className="w-full px-2.5 py-1.5 flex items-center justify-between text-[11px] font-bold text-slate-700 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800" > <span>{cat} ({items.length} |
| UI-0681 | 331 | event-prop | onClick | {() => toggleCategory(cat)} |
| UI-0682 | 344 | event-prop | onDragEnd | {() => onAddNode(item)} |
| UI-0683 | 345 | event-prop | onClick | {() => onAddNode(item)} |
| UI-0684 | 372 | control | button | <button key={item.type} onClick={() => onAddNode(item)} draggable onDragEnd={() => onAddNode(item)} className="w-8 h-8 rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 flex items-center justify-center hove |
| UI-0685 | 374 | event-prop | onClick | {() => onAddNode(item)} |
| UI-0686 | 376 | event-prop | onDragEnd | {() => onAddNode(item)} |

## components/modeler/ModelerPropertiesPanel.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0687 | 123 | control | button | <button onClick={() => setActiveTab('general')} className={&#96;px-2 py-1 rounded-md transition-all cursor-pointer whitespace-nowrap ${ activeTab === 'general' ? 'bg-slate-100 dark:bg-slate-800 text-emerald-600 dark:text-emerald-400 font-bo |
| UI-0688 | 124 | event-prop | onClick | {() => setActiveTab('general')} |
| UI-0689 | 132 | control | button | <button onClick={() => setActiveTab('actors')} className={&#96;px-2 py-1 rounded-md transition-all cursor-pointer whitespace-nowrap ${ activeTab === 'actors' ? 'bg-slate-100 dark:bg-slate-800 text-emerald-600 dark:text-emerald-400 font-bold |
| UI-0690 | 133 | event-prop | onClick | {() => setActiveTab('actors')} |
| UI-0691 | 141 | control | button | <button onClick={() => setActiveTab('execution')} className={&#96;px-2 py-1 rounded-md transition-all cursor-pointer whitespace-nowrap ${ activeTab === 'execution' ? 'bg-slate-100 dark:bg-slate-800 text-emerald-600 dark:text-emerald-400 fon |
| UI-0692 | 142 | event-prop | onClick | {() => setActiveTab('execution')} |
| UI-0693 | 150 | control | button | <button onClick={() => setActiveTab('iomapping')} className={&#96;px-2 py-1 rounded-md transition-all cursor-pointer whitespace-nowrap ${ activeTab === 'iomapping' ? 'bg-slate-100 dark:bg-slate-800 text-emerald-600 dark:text-emerald-400 fon |
| UI-0694 | 151 | event-prop | onClick | {() => setActiveTab('iomapping')} |
| UI-0695 | 159 | control | button | <button onClick={() => setActiveTab('multiInstance')} className={&#96;px-2 py-1 rounded-md transition-all cursor-pointer whitespace-nowrap ${ activeTab === 'multiInstance' ? 'bg-slate-100 dark:bg-slate-800 text-emerald-600 dark:text-emerald |
| UI-0696 | 160 | event-prop | onClick | {() => setActiveTab('multiInstance')} |
| UI-0697 | 180 | control | input | <input type="text" value={selectedNode ? selectedNode.id : currentProcess.key} onChange={(e) => { if (selectedNode) handlePropChange('id', e.target.value); else onUpdateProcess({ ...currentProcess, key: e.target.value }); }} className="w-fu |
| UI-0698 | 183 | event-prop | onChange | {(e) => { if (selectedNode) handlePropChange('id', e.target.value); else onUpdateProcess({ ...currentProcess, key: e.target.value }); }} |
| UI-0699 | 195 | control | input | <input type="text" value={selectedNode ? selectedNode.name : currentProcess.name} onChange={(e) => { if (selectedNode) handlePropChange('name', e.target.value); else onUpdateProcess({ ...currentProcess, name: e.target.value }); }} className |
| UI-0700 | 198 | event-prop | onChange | {(e) => { if (selectedNode) handlePropChange('name', e.target.value); else onUpdateProcess({ ...currentProcess, name: e.target.value }); }} |
| UI-0701 | 210 | control | select | <select value={currentProcess.engine} onChange={(e) => onUpdateProcess({ ...currentProcess, engine: e.target.value as any })} className="w-full px-2.5 py-1.5 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounde |
| UI-0702 | 212 | event-prop | onChange | {(e) => onUpdateProcess({ ...currentProcess, engine: e.target.value as any })} |
| UI-0703 | 224 | control | textarea | <textarea rows={3} value={selectedNode?.documentation &#124;&#124; currentProcess.description} onChange={(e) => { if (selectedNode) handlePropChange('documentation', e.target.value); else onUpdateProcess({ ...currentProcess, description: e. |
| UI-0704 | 227 | event-prop | onChange | {(e) => { if (selectedNode) handlePropChange('documentation', e.target.value); else onUpdateProcess({ ...currentProcess, description: e.target.value }); }} |
| UI-0705 | 238 | control | input | <input type="checkbox" defaultChecked className="rounded text-emerald-600 focus:ring-emerald-500 cursor-pointer" /> </div> </div> )} {/* ======================================================= */} {/* TAB 2: ACTORS & SWIMLANES (TÁC NHÂN & L |
| UI-0706 | 253 | control | select | <select value={selectedNode?.laneId &#124;&#124; ''} onChange={(e) => { const targetLane = availableLanes.find(l => l.id === e.target.value); handlePropChange('laneId', e.target.value); if (targetLane) { handlePropChange('actorName', target |
| UI-0707 | 255 | event-prop | onChange | {(e) => { const targetLane = availableLanes.find(l => l.id === e.target.value); handlePropChange('laneId', e.target.value); if (targetLane) { handlePropChange('actorName', targetLane.name); if (targetLane.actorRole && !selectedNode?.assigne |
| UI-0708 | 291 | control | button | <button onClick={() => handleRemoveCandidateGroup(role)} className="text-slate-400 hover:text-rose-500 text-xs cursor-pointer" > ✕  |
| UI-0709 | 292 | event-prop | onClick | {() => handleRemoveCandidateGroup(role)} |
| UI-0710 | 301 | control | input | <input type="text" id="newRoleInput" placeholder="Thêm Role (VD: NCLOS CA)..." className="flex-1 px-2 py-1 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded text-xs" onKeyDown={(e) => { if (e.key === 'Enter |
| UI-0711 | 306 | event-prop | onKeyDown | {(e) => { if (e.key === 'Enter') { const val = (e.target as HTMLInputElement).value.trim(); if (val) { handleAddCandidateGroup(val); (e.target as HTMLInputElement).value = ''; } } }} |
| UI-0712 | 316 | control | button | <button onClick={() => { const el = document.getElementById('newRoleInput') as HTMLInputElement; if (el && el.value.trim()) { handleAddCandidateGroup(el.value.trim()); el.value = ''; } }} className="px-2.5 py-1 bg-emerald-600 hover:bg-emera |
| UI-0713 | 317 | event-prop | onClick | {() => { const el = document.getElementById('newRoleInput') as HTMLInputElement; if (el && el.value.trim()) { handleAddCandidateGroup(el.value.trim()); el.value = ''; } }} |
| UI-0714 | 336 | control | input | <input type="text" value={selectedNode?.assigneeRole &#124;&#124; '= initiator'} onChange={(e) => handlePropChange('assigneeRole', e.target.value)} placeholder="VD: = initiator hoặc ${currentUser}" className="w-full px-2.5 py-1.5 bg-slate-5 |
| UI-0715 | 339 | event-prop | onChange | {(e) => handlePropChange('assigneeRole', e.target.value)} |
| UI-0716 | 350 | control | input | <input type="text" value={selectedNode?.formKey &#124;&#124; 'camunda-form:bpmn:UserTaskForm_HSGN'} onChange={(e) => handlePropChange('formKey', e.target.value)} placeholder="camunda-form:bpmn:UserTaskForm_01" className="w-full px-2.5 py-1. |
| UI-0717 | 353 | event-prop | onChange | {(e) => handlePropChange('formKey', e.target.value)} |
| UI-0718 | 364 | control | input | <input type="number" value={selectedNode?.slaHours &#124;&#124; 4} onChange={(e) => handlePropChange('slaHours', Number(e.target.value))} className="w-full px-2.5 py-1.5 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slat |
| UI-0719 | 367 | event-prop | onChange | {(e) => handlePropChange('slaHours', Number(e.target.value))} |
| UI-0720 | 377 | control | select | <select value={selectedNode?.viewEndpoint &#124;&#124; 'PSCF TaskView'} onChange={(e) => handlePropChange('viewEndpoint', e.target.value)} className="w-full px-2.5 py-1.5 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-sla |
| UI-0721 | 379 | event-prop | onChange | {(e) => handlePropChange('viewEndpoint', e.target.value)} |
| UI-0722 | 407 | control | input | <input type="text" value={selectedNode.jobType &#124;&#124; &#96;${selectedNode.id}-worker&#96;} onChange={(e) => handlePropChange('jobType', e.target.value)} placeholder="e.g. loan-disbursement-worker" className="w-full px-2 py-1 bg-white  |
| UI-0723 | 410 | event-prop | onChange | {(e) => handlePropChange('jobType', e.target.value)} |
| UI-0724 | 419 | control | input | <input type="number" value={selectedNode.jobRetries ?? 3} onChange={(e) => handlePropChange('jobRetries', Number(e.target.value))} className="w-full px-2 py-1 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded  |
| UI-0725 | 422 | event-prop | onChange | {(e) => handlePropChange('jobRetries', Number(e.target.value))} |
| UI-0726 | 440 | control | input | <input type="text" value={selectedNode.calledDecision?.decisionId &#124;&#124; 'creditScoreDMN'} onChange={(e) => handlePropChange('calledDecision', { ...selectedNode.calledDecision, decisionId: e.target.value, resultVariable: selectedNode. |
| UI-0727 | 443 | event-prop | onChange | {(e) => handlePropChange('calledDecision', { ...selectedNode.calledDecision, decisionId: e.target.value, resultVariable: selectedNode.calledDecision?.resultVariable &#124;&#124; 'creditRating' })} |
| UI-0728 | 451 | control | input | <input type="text" value={selectedNode.calledDecision?.resultVariable &#124;&#124; 'creditRating'} onChange={(e) => handlePropChange('calledDecision', { ...selectedNode.calledDecision, decisionId: selectedNode.calledDecision?.decisionId &#1 |
| UI-0729 | 454 | event-prop | onChange | {(e) => handlePropChange('calledDecision', { ...selectedNode.calledDecision, decisionId: selectedNode.calledDecision?.decisionId &#124;&#124; 'creditScoreDMN', resultVariable: e.target.value })} |
| UI-0730 | 459 | control | button | <button type="button" onClick={() => onOpenDmn?.(selectedNode.calledDecision?.decisionId &#124;&#124; 'creditScoreDMN')} className="w-full mt-1.5 py-1.5 px-2 bg-amber-600 hover:bg-amber-500 active:scale-[0.98] text-white rounded-lg text-[11 |
| UI-0731 | 461 | event-prop | onClick | {() => onOpenDmn?.(selectedNode.calledDecision?.decisionId &#124;&#124; 'creditScoreDMN')} |
| UI-0732 | 482 | control | input | <input type="text" value={selectedNode.calledElement?.processId &#124;&#124; 'sub_kyso_contract'} onChange={(e) => handlePropChange('calledElement', { processId: e.target.value, propagateAllChildVariables: true })} className="w-full px-2 py |
| UI-0733 | 485 | event-prop | onChange | {(e) => handlePropChange('calledElement', { processId: e.target.value, propagateAllChildVariables: true })} |
| UI-0734 | 491 | control | input | <input type="checkbox" defaultChecked className="rounded text-indigo-600" /> </div> </div> )} {/* Script Task */} {selectedNode?.type === 'scriptTask' && ( <div className="space-y-2"> <div>  |
| UI-0735 | 503 | control | select | <select value={selectedNode.scriptFormat &#124;&#124; 'feel'} onChange={(e) => handlePropChange('scriptFormat', e.target.value)} className="w-full px-2.5 py-1.5 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rou |
| UI-0736 | 505 | event-prop | onChange | {(e) => handlePropChange('scriptFormat', e.target.value)} |
| UI-0737 | 517 | control | textarea | <textarea rows={4} value={selectedNode.scriptBody &#124;&#124; '{ interestRate: loanAmount * 0.085 / 12, approved: true }'} onChange={(e) => handlePropChange('scriptBody', e.target.value)} className="w-full p-2 bg-slate-950 text-emerald-400 |
| UI-0738 | 520 | event-prop | onChange | {(e) => handlePropChange('scriptBody', e.target.value)} |
| UI-0739 | 533 | control | input | <input type="text" value={selectedNode.errorCode &#124;&#124; 'ERR_GATEWAY_TIMEOUT'} onChange={(e) => handlePropChange('errorCode', e.target.value)} className="w-full px-2.5 py-1.5 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark: |
| UI-0740 | 536 | event-prop | onChange | {(e) => handlePropChange('errorCode', e.target.value)} |
| UI-0741 | 546 | control | input | <input type="checkbox" defaultChecked className="rounded text-emerald-600 focus:ring-emerald-500" /> <span>Asynchronous Continuation (flowable:async="true")</span> </label> <label className="flex items-center gap-2 text-slate-600 dark:text- |
| UI-0742 | 550 | control | input | <input type="checkbox" defaultChecked className="rounded text-emerald-600 focus:ring-emerald-500" /> <span>Exclusive Job (flowable:exclusive="true")</span> </label> </div> </div> )} {/* ====================================================== |
| UI-0743 | 568 | control | button | <button onClick={handleAddInputMapping} className="flex items-center gap-1 text-[10.5px] font-semibold text-emerald-600 hover:text-emerald-700 cursor-pointer" > <Plus className="w-3 h-3" /> <span>Thêm Input</span>  |
| UI-0744 | 569 | event-prop | onClick | {handleAddInputMapping} |
| UI-0745 | 582 | control | input | <input type="text" defaultValue={inp.source} className="w-1/2 px-1.5 py-0.5 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded text-[10.5px] font-mono" placeholder="Source expr" /> <span className="text-slate-4 |
| UI-0746 | 589 | control | input | <input type="text" defaultValue={inp.target} className="w-1/2 px-1.5 py-0.5 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded text-[10.5px] font-mono" placeholder="Target var" /> <button onClick={() => handleR |
| UI-0747 | 595 | control | button | <button onClick={() => handleRemoveInputMapping(idx)} className="text-slate-400 hover:text-rose-500 p-0.5 cursor-pointer" > <Trash2 className="w-3 h-3" />  |
| UI-0748 | 596 | event-prop | onClick | {() => handleRemoveInputMapping(idx)} |
| UI-0749 | 612 | control | button | <button onClick={handleAddOutputMapping} className="flex items-center gap-1 text-[10.5px] font-semibold text-emerald-600 hover:text-emerald-700 cursor-pointer" > <Plus className="w-3 h-3" /> <span>Thêm Output</span>  |
| UI-0750 | 613 | event-prop | onClick | {handleAddOutputMapping} |
| UI-0751 | 625 | control | input | <input type="text" defaultValue={out.source} className="w-1/2 px-1.5 py-0.5 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded text-[10.5px] font-mono" placeholder="Source expr" /> <span className="text-slate-4 |
| UI-0752 | 632 | control | input | <input type="text" defaultValue={out.target} className="w-1/2 px-1.5 py-0.5 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded text-[10.5px] font-mono" placeholder="Target var" /> <button onClick={() => handleR |
| UI-0753 | 638 | control | button | <button onClick={() => handleRemoveOutputMapping(idx)} className="text-slate-400 hover:text-rose-500 p-0.5 cursor-pointer" > <Trash2 className="w-3 h-3" />  |
| UI-0754 | 639 | event-prop | onClick | {() => handleRemoveOutputMapping(idx)} |
| UI-0755 | 662 | control | button | <button onClick={() => handlePropChange('multiInstance', undefined)} className={&#96;p-1.5 rounded border text-center cursor-pointer ${ !selectedNode?.multiInstance ? 'border-emerald-500 bg-emerald-50 dark:bg-emerald-950/30 text-emerald-700 |
| UI-0756 | 663 | event-prop | onClick | {() => handlePropChange('multiInstance', undefined)} |
| UI-0757 | 670 | control | button | <button onClick={() => handlePropChange('multiInstance', { isSequential: false, inputCollection: '= customerList', inputElement: 'customer' })} className={&#96;p-1.5 rounded border text-center cursor-pointer ${ selectedNode?.multiInstance & |
| UI-0758 | 671 | event-prop | onClick | {() => handlePropChange('multiInstance', { isSequential: false, inputCollection: '= customerList', inputElement: 'customer' })} |
| UI-0759 | 678 | control | button | <button onClick={() => handlePropChange('multiInstance', { isSequential: true, inputCollection: '= approvalLevels', inputElement: 'level' })} className={&#96;p-1.5 rounded border text-center cursor-pointer ${ selectedNode?.multiInstance?.is |
| UI-0760 | 679 | event-prop | onClick | {() => handlePropChange('multiInstance', { isSequential: true, inputCollection: '= approvalLevels', inputElement: 'level' })} |
| UI-0761 | 692 | control | input | <input type="text" value={selectedNode.multiInstance.inputCollection &#124;&#124; '= customerList'} onChange={(e) => handlePropChange('multiInstance', { ...selectedNode.multiInstance, inputCollection: e.target.value })} className="w-full px |
| UI-0762 | 695 | event-prop | onChange | {(e) => handlePropChange('multiInstance', { ...selectedNode.multiInstance, inputCollection: e.target.value })} |
| UI-0763 | 701 | control | input | <input type="text" value={selectedNode.multiInstance.inputElement &#124;&#124; 'customer'} onChange={(e) => handlePropChange('multiInstance', { ...selectedNode.multiInstance, inputElement: e.target.value })} className="w-full px-2 py-1 bg-w |
| UI-0764 | 704 | event-prop | onChange | {(e) => handlePropChange('multiInstance', { ...selectedNode.multiInstance, inputElement: e.target.value })} |
| UI-0765 | 710 | control | input | <input type="text" placeholder="VD: = nrOfCompletedInstances >= 1" value={selectedNode.multiInstance.completionCondition &#124;&#124; ''} onChange={(e) => handlePropChange('multiInstance', { ...selectedNode.multiInstance, completionConditio |
| UI-0766 | 714 | event-prop | onChange | {(e) => handlePropChange('multiInstance', { ...selectedNode.multiInstance, completionCondition: e.target.value })} |
| UI-0767 | 733 | control | input | <input type="checkbox" defaultChecked className="rounded text-emerald-600" /> <span className="text-slate-700 dark:text-slate-300">ecm.bpm.{currentProcess.key.toLowerCase()}.{ev}</span> </label> ))} </div> </div>  |

## components/operation/OperationModule.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0768 | 248 | control | button | <button onClick={() => setShowMigrateModal(true)} disabled={selectedInstanceIds.length === 0} className="flex items-center gap-1.5 px-3.5 py-1.5 bg-emerald-600 hover:bg-emerald-500 active:scale-[0.98] disabled:opacity-40 text-white rounded- |
| UI-0769 | 249 | event-prop | onClick | {() => setShowMigrateModal(true)} |
| UI-0770 | 256 | control | button | <button onClick={() => setShowMoveModal(true)} disabled={selectedInstanceIds.length === 0} className="flex items-center gap-1.5 px-3.5 py-1.5 bg-blue-600 hover:bg-blue-500 active:scale-[0.98] disabled:opacity-40 text-white rounded-lg text-x |
| UI-0771 | 257 | event-prop | onClick | {() => setShowMoveModal(true)} |
| UI-0772 | 264 | control | button | <button onClick={handleCancelSelected} disabled={selectedInstanceIds.length === 0} className="flex items-center gap-1.5 px-3.5 py-1.5 bg-rose-600 hover:bg-rose-500 active:scale-[0.98] disabled:opacity-40 text-white rounded-lg text-xs font-s |
| UI-0773 | 265 | event-prop | onClick | {handleCancelSelected} |
| UI-0774 | 281 | control | select | <select value={filterVersion} onChange={(e) => setFilterVersion(e.target.value)} className="w-full px-2 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded text-xs" > <option value="ALL">ALL</option> <opt |
| UI-0775 | 283 | event-prop | onChange | {(e) => setFilterVersion(e.target.value)} |
| UI-0776 | 295 | control | select | <select value={filterTaskName} onChange={(e) => setFilterTaskName(e.target.value)} className="w-full px-2 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded text-xs" > <option value="ALL">ALL</option> {c |
| UI-0777 | 297 | event-prop | onChange | {(e) => setFilterTaskName(e.target.value)} |
| UI-0778 | 309 | control | input | <input type="text" placeholder="e.g. 6755399..." value={filterId} onChange={(e) => setFilterId(e.target.value)} className="w-full px-2 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded text-xs font-mono |
| UI-0779 | 313 | event-prop | onChange | {(e) => setFilterId(e.target.value)} |
| UI-0780 | 320 | control | input | <input type="text" placeholder="e.g. SDD-0000426..." value={filterRecordCode} onChange={(e) => setFilterRecordCode(e.target.value)} className="w-full px-2 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 round |
| UI-0781 | 324 | event-prop | onChange | {(e) => setFilterRecordCode(e.target.value)} |
| UI-0782 | 333 | control | input | <input type="checkbox" checked={filterActive} onChange={(e) => setFilterActive(e.target.checked)} className="rounded text-emerald-600 focus:ring-emerald-500" /> <span className="text-slate-700 dark:text-slate-300">Active ({activeCount})</sp |
| UI-0783 | 336 | event-prop | onChange | {(e) => setFilterActive(e.target.checked)} |
| UI-0784 | 342 | control | input | <input type="checkbox" checked={filterIssues} onChange={(e) => setFilterIssues(e.target.checked)} className="rounded text-emerald-600 focus:ring-emerald-500" /> <span className="text-slate-700 dark:text-slate-300">Issues (0)</span> </label> |
| UI-0785 | 345 | event-prop | onChange | {(e) => setFilterIssues(e.target.checked)} |
| UI-0786 | 356 | control | input | <input type="checkbox" checked={filterCompleted} onChange={(e) => setFilterCompleted(e.target.checked)} className="rounded text-emerald-600 focus:ring-emerald-500" /> <span className="text-slate-700 dark:text-slate-300">Completed ({complete |
| UI-0787 | 359 | event-prop | onChange | {(e) => setFilterCompleted(e.target.checked)} |
| UI-0788 | 365 | control | input | <input type="checkbox" checked={filterCanceled} onChange={(e) => setFilterCanceled(e.target.checked)} className="rounded text-emerald-600 focus:ring-emerald-500" /> <span className="text-slate-700 dark:text-slate-300">Canceled ({canceledCou |
| UI-0789 | 368 | event-prop | onChange | {(e) => setFilterCanceled(e.target.checked)} |
| UI-0790 | 377 | control | button | <button onClick={() => { setFilterVersion('ALL'); setFilterTaskName('ALL'); setFilterId(''); setFilterRecordCode(''); setFilterActive(true); setFilterIssues(true); setFilterCompleted(true); setFilterCanceled(false); }} className="flex-1 py- |
| UI-0791 | 378 | event-prop | onClick | {() => { setFilterVersion('ALL'); setFilterTaskName('ALL'); setFilterId(''); setFilterRecordCode(''); setFilterActive(true); setFilterIssues(true); setFilterCompleted(true); setFilterCanceled(false); }} |
| UI-0792 | 392 | control | button | <button onClick={() => { // applied }} className="flex-1 py-1.5 bg-emerald-700 hover:bg-emerald-800 text-white rounded font-semibold" > Apply Filter  |
| UI-0793 | 393 | event-prop | onClick | {() => { // applied }} |
| UI-0794 | 429 | control | button | <button onClick={() => { setFilterActive(true); setFilterCompleted(true); setFilterCanceled(true); }} className={&#96;px-2 py-0.5 rounded font-semibold transition-all cursor-pointer ${ filterActive && filterCompleted && filterCanceled ? 'bg |
| UI-0795 | 430 | event-prop | onClick | {() => { setFilterActive(true); setFilterCompleted(true); setFilterCanceled(true); }} |
| UI-0796 | 443 | control | button | <button onClick={() => { setFilterActive(true); setFilterCompleted(false); setFilterCanceled(false); }} className={&#96;px-2 py-0.5 rounded font-semibold transition-all cursor-pointer ${ filterActive && !filterCompleted && !filterCanceled ? |
| UI-0797 | 444 | event-prop | onClick | {() => { setFilterActive(true); setFilterCompleted(false); setFilterCanceled(false); }} |
| UI-0798 | 457 | control | button | <button onClick={() => { setFilterActive(false); setFilterCompleted(true); setFilterCanceled(false); }} className={&#96;px-2 py-0.5 rounded font-semibold transition-all cursor-pointer ${ !filterActive && filterCompleted && !filterCanceled ? |
| UI-0799 | 458 | event-prop | onClick | {() => { setFilterActive(false); setFilterCompleted(true); setFilterCanceled(false); }} |
| UI-0800 | 473 | control | button | <button onClick={() => setShowSummaryDashboard(!showSummaryDashboard)} className="p-1.5 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 rounded-lg hover:bg-slate-100 dark:hover:bg-slate-800 cursor-pointer transition-colors fle |
| UI-0801 | 474 | event-prop | onClick | {() => setShowSummaryDashboard(!showSummaryDashboard)} |
| UI-0802 | 590 | event-prop | onClick | {() => { setFilterActive(true); setFilterCompleted(false); setFilterCanceled(false); }} |
| UI-0803 | 628 | event-prop | onClick | {() => { setFilterActive(false); setFilterCompleted(true); setFilterCanceled(false); }} |
| UI-0804 | 698 | control | button | <button onClick={() => { setFilterActive(true); setFilterCompleted(true); setFilterCanceled(true); setFilterVersion('ALL'); setFilterTaskName('ALL'); }} className="text-purple-600 dark:text-purple-400 hover:underline font-semibold cursor-po |
| UI-0805 | 699 | event-prop | onClick | {() => { setFilterActive(true); setFilterCompleted(true); setFilterCanceled(true); setFilterVersion('ALL'); setFilterTaskName('ALL'); }} |
| UI-0806 | 723 | control | input | <input type="checkbox" checked={selectedInstanceIds.length > 0 && selectedInstanceIds.length === filteredInstances.length} onChange={handleToggleSelectAll} className="rounded text-emerald-900 border-white" /> </th> <th className="py-2.5 px- |
| UI-0807 | 726 | event-prop | onChange | {handleToggleSelectAll} |
| UI-0808 | 748 | event-prop | onClick | {() => setActiveInstanceDetail(inst)} |
| UI-0809 | 753 | event-prop | onClick | {(e) => e.stopPropagation()} |
| UI-0810 | 754 | control | input | <input type="checkbox" checked={isSelected} onChange={() => handleToggleSelectRow(inst.id)} className="rounded text-emerald-600 focus:ring-emerald-500" /> </td> <td className="py-2 px-3 font-mono text-emerald-700 dark:text-emerald-400 font- |
| UI-0811 | 757 | event-prop | onChange | {() => handleToggleSelectRow(inst.id)} |
| UI-0812 | 796 | event-prop | onClick | {(e) => e.stopPropagation()} |
| UI-0813 | 797 | control | button | <button onClick={() => { const updated = instances.map(i => i.id === inst.id ? { ...i, status: 'CANCELED' as const } : i); onUpdateInstances(updated); }} className="p-1 text-red-500 hover:text-red-700 hover:bg-red-50 dark:hover:bg-red-950/4 |
| UI-0814 | 798 | event-prop | onClick | {() => { const updated = instances.map(i => i.id === inst.id ? { ...i, status: 'CANCELED' as const } : i); onUpdateInstances(updated); }} |
| UI-0815 | 831 | control | button | <button onClick={() => setShowMigrateModal(false)} className="text-slate-400 hover:text-slate-600">✕ |
| UI-0816 | 831 | event-prop | onClick | {() => setShowMigrateModal(false)} |
| UI-0817 | 839 | control | select | <select value={targetVersion} onChange={(e) => setTargetVersion(Number(e.target.value))} className="w-full px-2 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded text-xs font-semibold" > <option value={ |
| UI-0818 | 841 | event-prop | onChange | {(e) => setTargetVersion(Number(e.target.value))} |
| UI-0819 | 853 | control | button | <button onClick={() => setShowMigrateModal(false)} className="px-3 py-1.5 border border-slate-300 rounded text-xs font-semibold"> Hủy  |
| UI-0820 | 853 | event-prop | onClick | {() => setShowMigrateModal(false)} |
| UI-0821 | 856 | control | button | <button onClick={handleMigrateInstances} className="px-4 py-1.5 bg-emerald-700 text-white rounded text-xs font-semibold hover:bg-emerald-800"> Xác nhận Migrate  |
| UI-0822 | 856 | event-prop | onClick | {handleMigrateInstances} |
| UI-0823 | 873 | control | button | <button onClick={() => setShowMoveModal(false)} className="text-slate-400 hover:text-slate-600">✕ |
| UI-0824 | 873 | event-prop | onClick | {() => setShowMoveModal(false)} |
| UI-0825 | 881 | control | select | <select value={targetStep} onChange={(e) => setTargetStep(e.target.value)} className="w-full px-2 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded text-xs font-semibold" > {currentProcess.nodes.filter( |
| UI-0826 | 883 | event-prop | onChange | {(e) => setTargetStep(e.target.value)} |
| UI-0827 | 893 | control | button | <button onClick={() => setShowMoveModal(false)} className="px-3 py-1.5 border border-slate-300 rounded text-xs font-semibold"> Hủy  |
| UI-0828 | 893 | event-prop | onClick | {() => setShowMoveModal(false)} |
| UI-0829 | 896 | control | button | <button onClick={handleMoveTokens} className="px-4 py-1.5 bg-emerald-700 text-white rounded text-xs font-semibold hover:bg-emerald-800"> Move Token  |
| UI-0830 | 896 | event-prop | onClick | {handleMoveTokens} |
| UI-0831 | 912 | control | button | <button onClick={() => setActiveInstanceDetail(null)} className="text-slate-400 hover:text-slate-600 p-1">✕ |
| UI-0832 | 912 | event-prop | onClick | {() => setActiveInstanceDetail(null)} |

## components/optimize/OptimizeModule.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0833 | 87 | control | button | <button onClick={() => setActiveTab('summary')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer ${ activeTab === 'summary' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 shadow-xs' : 'text-slate-600 da |
| UI-0834 | 88 | event-prop | onClick | {() => setActiveTab('summary')} |
| UI-0835 | 98 | control | button | <button onClick={() => setActiveTab('charts')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer ${ activeTab === 'charts' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 shadow-xs' : 'text-slate-600 dark |
| UI-0836 | 99 | event-prop | onClick | {() => setActiveTab('charts')} |
| UI-0837 | 109 | control | button | <button onClick={() => setActiveTab('throughput')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer flex items-center gap-1.5 ${ activeTab === 'throughput' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 |
| UI-0838 | 110 | event-prop | onClick | {() => setActiveTab('throughput')} |
| UI-0839 | 122 | control | button | <button onClick={() => setActiveTab('health')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer flex items-center gap-1.5 ${ activeTab === 'health' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 shadow- |
| UI-0840 | 123 | event-prop | onClick | {() => setActiveTab('health')} |
| UI-0841 | 134 | control | button | <button onClick={() => setActiveTab('heatmap')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer flex items-center gap-1.5 ${ activeTab === 'heatmap' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 shado |
| UI-0842 | 135 | event-prop | onClick | {() => setActiveTab('heatmap')} |
| UI-0843 | 149 | control | button | <button onClick={() => { // reset filters }} className="px-3 py-1.5 border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 hover:bg-slate-50 dark:hover:bg-slate-750 text-slate-700 dark:text-slate-300 rounded-lg text-xs fon |
| UI-0844 | 150 | event-prop | onClick | {() => { // reset filters }} |
| UI-0845 | 157 | control | button | <button onClick={() => { // apply filters }} className="px-3.5 py-1.5 bg-emerald-600 hover:bg-emerald-500 text-white rounded-lg text-xs font-semibold shadow-xs transition-all active:scale-[0.98] cursor-pointer" > ✓ Áp Dụng  |
| UI-0846 | 158 | event-prop | onClick | {() => { // apply filters }} |
| UI-0847 | 205 | event-prop | onClick | {() => setActiveTab('throughput')} |
| UI-0848 | 252 | event-prop | onClick | {() => setActiveTab('heatmap')} |
| UI-0849 | 292 | event-prop | onClick | {() => setActiveTab('charts')} |
| UI-0850 | 336 | event-prop | onClick | {() => setActiveTab('health')} |
| UI-0851 | 381 | event-prop | onNotify | {(msg) => { // notification feedback }} |
| UI-0852 | 384 | event-prop | onOpenHeatmap | {(procKey) => { setSelectedProcessKey(procKey); setActiveTab('heatmap'); }} |
| UI-0853 | 694 | event-prop | onMouseEnter | {(e) => { if (heatmap) { setHoveredTaskHeatmap({ task: heatmap, x: node.x + node.width / 2, y: node.y + node.height, }); } }} |
| UI-0854 | 703 | event-prop | onMouseLeave | {() => setHoveredTaskHeatmap(null)} |
| UI-0855 | 812 | control | select | <select value={selectedProcessKey} onChange={(e) => setSelectedProcessKey(e.target.value)} className="w-full px-2.5 py-2 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded text-xs font-semibold" > <option value |
| UI-0856 | 814 | event-prop | onChange | {(e) => setSelectedProcessKey(e.target.value)} |
| UI-0857 | 825 | control | select | <select value={timeUnit} onChange={(e) => setTimeUnit(e.target.value)} className="w-full px-2.5 py-2 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded text-xs" > <option value="Giờ">Giờ</option> <option value= |
| UI-0858 | 827 | event-prop | onChange | {(e) => setTimeUnit(e.target.value)} |
| UI-0859 | 838 | control | input | <input type="number" value={thresholdHours} onChange={(e) => setThresholdHours(Number(e.target.value))} className="w-full px-2.5 py-2 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded text-xs font-mono" /> </d |
| UI-0860 | 841 | event-prop | onChange | {(e) => setThresholdHours(Number(e.target.value))} |
| UI-0861 | 848 | control | input | <input type="text" value={dateRange} onChange={(e) => setDateRange(e.target.value)} className="w-full px-2.5 py-2 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded text-xs font-mono" /> </div> <button onClick= |
| UI-0862 | 851 | event-prop | onChange | {(e) => setDateRange(e.target.value)} |
| UI-0863 | 856 | control | button | <button onClick={() => { // Reload Heatmap }} className="w-full py-2.5 bg-emerald-700 hover:bg-emerald-800 text-white font-semibold rounded text-xs shadow-sm transition-colors cursor-pointer mt-2" > Tải Heatmap  |
| UI-0864 | 857 | event-prop | onClick | {() => { // Reload Heatmap }} |

## components/optimize/ProcessHealthReport.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0865 | 319 | control | button | <button key={tr} onClick={() => setTimeRange(tr)} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer text-xs ${ timeRange === tr ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 shadow-xs' : 'text-slate-600 |
| UI-0866 | 321 | event-prop | onClick | {() => setTimeRange(tr)} |
| UI-0867 | 335 | control | select | <select value={selectedProcess} onChange={(e) => setSelectedProcess(e.target.value)} className="bg-slate-50 dark:bg-slate-800 border border-slate-200/80 dark:border-slate-700/70 text-slate-700 dark:text-slate-200 text-xs font-semibold px-3  |
| UI-0868 | 337 | event-prop | onChange | {(e) => setSelectedProcess(e.target.value)} |
| UI-0869 | 349 | control | button | <button onClick={handleExportCSV} className="flex items-center gap-1.5 px-3 py-1.5 bg-white dark:bg-slate-800 hover:bg-slate-50 dark:hover:bg-slate-750 text-slate-700 dark:text-slate-200 border border-slate-200/80 dark:border-slate-700/80 r |
| UI-0870 | 350 | event-prop | onClick | {handleExportCSV} |
| UI-0871 | 505 | control | button | <button onClick={() => setActiveChartType('OVERALL')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer ${ activeChartType === 'OVERALL' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 shadow-xs' : 'text- |
| UI-0872 | 506 | event-prop | onClick | {() => setActiveChartType('OVERALL')} |
| UI-0873 | 515 | control | button | <button onClick={() => setActiveChartType('MULTI_PROCESS')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer ${ activeChartType === 'MULTI_PROCESS' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 shadow- |
| UI-0874 | 516 | event-prop | onClick | {() => setActiveChartType('MULTI_PROCESS')} |
| UI-0875 | 525 | control | button | <button onClick={() => setActiveChartType('ROOT_CAUSE')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer ${ activeChartType === 'ROOT_CAUSE' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 shadow-xs' :  |
| UI-0876 | 526 | event-prop | onClick | {() => setActiveChartType('ROOT_CAUSE')} |
| UI-0877 | 693 | control | input | <input type="text" placeholder="Tìm mã instance, lỗi..." value={incidentSearchTerm} onChange={(e) => setIncidentSearchTerm(e.target.value)} className="pl-7 pr-3 py-1.5 text-xs bg-white dark:bg-slate-900 border border-slate-200 dark:border-s |
| UI-0878 | 697 | event-prop | onChange | {(e) => setIncidentSearchTerm(e.target.value)} |
| UI-0879 | 705 | control | button | <button key={sev} onClick={() => setSeverityFilter(sev)} className={&#96;px-2.5 py-1 rounded-lg text-[11px] font-semibold transition-all cursor-pointer ${ severityFilter === sev ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-1 |
| UI-0880 | 707 | event-prop | onClick | {() => setSeverityFilter(sev)} |
| UI-0881 | 794 | control | button | <button onClick={() => handleManualRetry(inc.id)} className="inline-flex items-center gap-1 px-2.5 py-1 rounded-lg bg-emerald-600 hover:bg-emerald-500 active:scale-[0.98] text-white text-[11px] font-semibold transition-all shadow-2xs cursor |
| UI-0882 | 795 | event-prop | onClick | {() => handleManualRetry(inc.id)} |

## components/optimize/ProcessThroughputDashboard.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0883 | 298 | control | select | <select value={selectedProcess} onChange={(e) => setSelectedProcess(e.target.value)} className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg px-2.5 py-1 text-xs font-semibold text-slate-800 dark:text-s |
| UI-0884 | 300 | event-prop | onChange | {(e) => setSelectedProcess(e.target.value)} |
| UI-0885 | 315 | control | button | <button key={tr} onClick={() => setTimeRange(tr)} className={&#96;px-2.5 py-1 rounded-lg text-xs font-semibold transition-all cursor-pointer ${ timeRange === tr ? 'bg-white dark:bg-slate-900 text-emerald-700 dark:text-emerald-400 shadow-2xs |
| UI-0886 | 317 | event-prop | onClick | {() => setTimeRange(tr)} |
| UI-0887 | 330 | control | button | <button onClick={handleExport} className="flex items-center gap-1 px-2.5 py-1.5 text-xs font-semibold text-slate-700 dark:text-slate-300 hover:text-emerald-700 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-750 rounde |
| UI-0888 | 331 | event-prop | onClick | {handleExport} |
| UI-0889 | 340 | control | button | <button onClick={handleRefresh} className="p-2 text-slate-600 dark:text-slate-300 hover:text-emerald-600 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 rounded-xl border border-slate-200 dark:border-slate-700 tran |
| UI-0890 | 341 | event-prop | onClick | {handleRefresh} |
| UI-0891 | 462 | control | button | <button onClick={() => setActiveMetricTab('cycle-time')} className={&#96;px-3.5 py-2 rounded-xl font-semibold transition-all cursor-pointer flex items-center gap-1.5 whitespace-nowrap ${ activeMetricTab === 'cycle-time' ? 'bg-emerald-600 te |
| UI-0892 | 463 | event-prop | onClick | {() => setActiveMetricTab('cycle-time')} |
| UI-0893 | 474 | control | button | <button onClick={() => setActiveMetricTab('comparison')} className={&#96;px-3.5 py-2 rounded-xl font-semibold transition-all cursor-pointer flex items-center gap-1.5 whitespace-nowrap ${ activeMetricTab === 'comparison' ? 'bg-emerald-600 te |
| UI-0894 | 475 | event-prop | onClick | {() => setActiveMetricTab('comparison')} |
| UI-0895 | 486 | control | button | <button onClick={() => setActiveMetricTab('stages')} className={&#96;px-3.5 py-2 rounded-xl font-semibold transition-all cursor-pointer flex items-center gap-1.5 whitespace-nowrap ${ activeMetricTab === 'stages' ? 'bg-emerald-600 text-white |
| UI-0896 | 487 | event-prop | onClick | {() => setActiveMetricTab('stages')} |
| UI-0897 | 498 | control | button | <button onClick={() => setActiveMetricTab('percentiles')} className={&#96;px-3.5 py-2 rounded-xl font-semibold transition-all cursor-pointer flex items-center gap-1.5 whitespace-nowrap ${ activeMetricTab === 'percentiles' ? 'bg-emerald-600  |
| UI-0898 | 499 | event-prop | onClick | {() => setActiveMetricTab('percentiles')} |
| UI-0899 | 672 | control | button | <button key={proc.key} onClick={() => setVisibleProcesses(prev => ({ ...prev, [proc.key]: !isVis }))} className={&#96;px-2.5 py-1 rounded-lg font-semibold text-[11px] border transition-all cursor-pointer flex items-center gap-1.5 ${ isVis ? |
| UI-0900 | 674 | event-prop | onClick | {() => setVisibleProcesses(prev => ({ ...prev, [proc.key]: !isVis }))} |
| UI-0901 | 915 | control | input | <input type="text" placeholder="Tìm tên hoặc mã quy trình..." value={tableSearch} onChange={(e) => setTableSearch(e.target.value)} className="pl-8 pr-3 py-1.5 text-xs bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-7 |
| UI-0902 | 919 | event-prop | onChange | {(e) => setTableSearch(e.target.value)} |
| UI-0903 | 979 | control | button | <button onClick={() => onOpenHeatmap(item.key)} className="px-2 py-1 bg-slate-100 hover:bg-slate-200 dark:bg-slate-800 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-300 rounded text-[11px] font-semibold cursor-pointer transition-co |
| UI-0904 | 980 | event-prop | onClick | {() => onOpenHeatmap(item.key)} |

## components/portal/ClientPortalModule.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-0905 | 676 | control | select | <select value={currentPersona.id} onChange={(e) => handlePersonaChange(e.target.value)} className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 text-slate-800 dark:text-slate-200 rounded-lg px-2.5 py-1 text-xs fo |
| UI-0906 | 678 | event-prop | onChange | {(e) => handlePersonaChange(e.target.value)} |
| UI-0907 | 691 | control | button | <button onClick={onOpenAdminConsole} className="flex items-center gap-1.5 px-3 py-1.5 bg-slate-800 hover:bg-slate-900 text-white dark:bg-slate-700 dark:hover:bg-slate-600 rounded-lg text-xs font-semibold shadow-2xs transition-all cursor-poi |
| UI-0908 | 692 | event-prop | onClick | {onOpenAdminConsole} |
| UI-0909 | 708 | control | button | <button onClick={() => setActiveTab('dashboard')} className={&#96;py-3 px-3.5 text-xs font-semibold border-b-2 transition-all cursor-pointer flex items-center gap-2 whitespace-nowrap ${ activeTab === 'dashboard' ? 'border-emerald-600 text-e |
| UI-0910 | 709 | event-prop | onClick | {() => setActiveTab('dashboard')} |
| UI-0911 | 719 | control | button | <button onClick={() => setActiveTab('metrics-dashboard')} className={&#96;py-3 px-3.5 text-xs font-semibold border-b-2 transition-all cursor-pointer flex items-center gap-1.5 whitespace-nowrap ${ activeTab === 'metrics-dashboard' ? 'border- |
| UI-0912 | 720 | event-prop | onClick | {() => setActiveTab('metrics-dashboard')} |
| UI-0913 | 732 | control | button | <button onClick={() => { setActiveTab('new-request'); setWizardStep(1); }} className={&#96;py-3 px-3.5 text-xs font-semibold border-b-2 transition-all cursor-pointer flex items-center gap-2 whitespace-nowrap ${ activeTab === 'new-request' ? |
| UI-0914 | 733 | event-prop | onClick | {() => { setActiveTab('new-request'); setWizardStep(1); }} |
| UI-0915 | 747 | control | button | <button onClick={() => setActiveTab('my-requests')} className={&#96;py-3 px-3.5 text-xs font-semibold border-b-2 transition-all cursor-pointer flex items-center gap-2 whitespace-nowrap ${ activeTab === 'my-requests' ? 'border-emerald-600 te |
| UI-0916 | 748 | event-prop | onClick | {() => setActiveTab('my-requests')} |
| UI-0917 | 761 | control | button | <button onClick={() => setActiveTab('officer-desk')} className={&#96;py-3 px-3.5 text-xs font-semibold border-b-2 transition-all cursor-pointer flex items-center gap-2 whitespace-nowrap ${ activeTab === 'officer-desk' ? 'border-emerald-600  |
| UI-0918 | 762 | event-prop | onClick | {() => setActiveTab('officer-desk')} |
| UI-0919 | 773 | control | button | <button onClick={() => setActiveTab('tracking-lookup')} className={&#96;py-3 px-3.5 text-xs font-semibold border-b-2 transition-all cursor-pointer flex items-center gap-2 whitespace-nowrap ${ activeTab === 'tracking-lookup' ? 'border-emeral |
| UI-0920 | 774 | event-prop | onClick | {() => setActiveTab('tracking-lookup')} |
| UI-0921 | 798 | event-prop | onNavigateTab | {setActiveTab} |
| UI-0922 | 799 | event-prop | onFilterStatus | {(st) => { setStatusFilter(st); setActiveTab('my-requests'); }} |
| UI-0923 | 803 | event-prop | onSelectRequest | {(id) => { setSelectedRequestId(id); setActiveTab('my-requests'); }} |
| UI-0924 | 807 | event-prop | onNotify | {showToast} |
| UI-0925 | 831 | control | button | <button onClick={() => setActiveTab('metrics-dashboard')} className="flex items-center gap-1.5 px-3.5 py-2 bg-emerald-500/30 hover:bg-emerald-500/50 text-white border border-emerald-400/40 rounded-xl text-xs font-semibold transition-all sha |
| UI-0926 | 832 | event-prop | onClick | {() => setActiveTab('metrics-dashboard')} |
| UI-0927 | 838 | control | button | <button onClick={() => { setActiveTab('new-request'); setWizardStep(1); }} className="flex items-center gap-2 px-4 py-2 bg-white text-emerald-800 hover:bg-emerald-50 rounded-xl text-xs font-bold transition-all shadow-md cursor-pointer" > <P |
| UI-0928 | 839 | event-prop | onClick | {() => { setActiveTab('new-request'); setWizardStep(1); }} |
| UI-0929 | 848 | control | button | <button onClick={() => setActiveTab('my-requests')} className="flex items-center gap-2 px-3.5 py-2 bg-emerald-800/60 hover:bg-emerald-800 text-white border border-emerald-500/40 rounded-xl text-xs font-semibold transition-all cursor-pointer |
| UI-0930 | 849 | event-prop | onClick | {() => setActiveTab('my-requests')} |
| UI-0931 | 860 | event-prop | onClick | {() => { setStatusFilter('ALL'); setActiveTab('my-requests'); }} |
| UI-0932 | 879 | event-prop | onClick | {() => { setStatusFilter('IN_REVIEW'); setActiveTab('my-requests'); }} |
| UI-0933 | 898 | event-prop | onClick | {() => { setStatusFilter('ADDITIONAL_DOCS_REQUIRED'); setActiveTab('my-requests'); }} |
| UI-0934 | 919 | event-prop | onClick | {() => { setStatusFilter('APPROVED'); setActiveTab('my-requests'); }} |
| UI-0935 | 950 | control | button | <button onClick={() => setActiveTab('my-requests')} className="text-xs font-semibold text-emerald-700 dark:text-emerald-400 hover:underline flex items-center gap-1 cursor-pointer" > <span>Xem toàn bộ ({requests.length})</span> <ChevronRight |
| UI-0936 | 951 | event-prop | onClick | {() => setActiveTab('my-requests')} |
| UI-0937 | 977 | event-prop | onClick | {() => { setSelectedRequestId(req.id); setActiveTab('my-requests'); }} |
| UI-0938 | 1011 | control | button | <button onClick={(e) => { e.stopPropagation(); setSelectedRequestId(req.id); setActiveTab('my-requests'); }} className="px-2.5 py-1 bg-slate-100 dark:bg-slate-800 hover:bg-emerald-50 dark:hover:bg-emerald-950 text-slate-700 dark:text-slate- |
| UI-0939 | 1012 | event-prop | onClick | {(e) => { e.stopPropagation(); setSelectedRequestId(req.id); setActiveTab('my-requests'); }} |
| UI-0940 | 1075 | control | button | <button onClick={() => { const targetKey = svc.processKey === 'SDD' ? 'SDD' : 'NCLOS'; handleSelectProcess(targetKey); setNewRequestService(svc.key); setActiveTab('new-request'); setWizardStep(1); }} className="w-full py-1.5 bg-emerald-50 h |
| UI-0941 | 1076 | event-prop | onClick | {() => { const targetKey = svc.processKey === 'SDD' ? 'SDD' : 'NCLOS'; handleSelectProcess(targetKey); setNewRequestService(svc.key); setActiveTab('new-request'); setWizardStep(1); }} |
| UI-0942 | 1124 | control | button | <button onClick={() => setWizardStep(item.step)} className={&#96;flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-semibold transition-all cursor-pointer ${ wizardStep === item.step ? 'bg-emerald-600 text-white shadow-xs' : wiza |
| UI-0943 | 1125 | event-prop | onClick | {() => setWizardStep(item.step)} |
| UI-0944 | 1173 | event-prop | onClick | {() => handleSelectProcess(proc.key)} |
| UI-0945 | 1234 | control | button | <button type="button" onClick={handleGenerateBusinessKey} className="text-[11px] font-semibold text-emerald-700 dark:text-emerald-400 hover:underline flex items-center gap-1 cursor-pointer" > <RefreshCw className="w-3 h-3" /> <span>Tạo mã t |
| UI-0946 | 1236 | event-prop | onClick | {handleGenerateBusinessKey} |
| UI-0947 | 1243 | control | input | <input type="text" value={businessKey} onChange={(e) => setBusinessKey(e.target.value)} className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-xs font-mono font-bold text-slate-9 |
| UI-0948 | 1246 | event-prop | onChange | {(e) => setBusinessKey(e.target.value)} |
| UI-0949 | 1259 | control | select | <select value={branchCode} onChange={(e) => setBranchCode(e.target.value)} className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-xs font-semibold focus:outline-none focus:ring-2 |
| UI-0950 | 1261 | event-prop | onChange | {(e) => setBranchCode(e.target.value)} |
| UI-0951 | 1285 | control | input | <input type="text" value={newCustomerOrg} onChange={(e) => setNewCustomerOrg(e.target.value)} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg text-xs font-medium" placeholde |
| UI-0952 | 1288 | event-prop | onChange | {(e) => setNewCustomerOrg(e.target.value)} |
| UI-0953 | 1298 | control | input | <input type="text" value={newCustomerTaxId} onChange={(e) => setNewCustomerTaxId(e.target.value)} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg text-xs font-mono font-bold |
| UI-0954 | 1301 | event-prop | onChange | {(e) => setNewCustomerTaxId(e.target.value)} |
| UI-0955 | 1310 | control | input | <input type="text" value={newCustomerName} onChange={(e) => setNewCustomerName(e.target.value)} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg text-xs" /> </div> <div class |
| UI-0956 | 1313 | event-prop | onChange | {(e) => setNewCustomerName(e.target.value)} |
| UI-0957 | 1322 | control | input | <input type="text" value={newCustomerPhone} onChange={(e) => setNewCustomerPhone(e.target.value)} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg text-xs font-mono" /> </div |
| UI-0958 | 1325 | event-prop | onChange | {(e) => setNewCustomerPhone(e.target.value)} |
| UI-0959 | 1334 | control | input | <input type="email" value={newCustomerEmail} onChange={(e) => setNewCustomerEmail(e.target.value)} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg text-xs" /> </div> </div>  |
| UI-0960 | 1337 | event-prop | onChange | {(e) => setNewCustomerEmail(e.target.value)} |
| UI-0961 | 1349 | control | button | <button onClick={() => setWizardStep(2)} className="flex items-center gap-1.5 px-4 py-2 bg-emerald-600 hover:bg-emerald-700 text-white rounded-lg text-xs font-semibold cursor-pointer shadow-xs transition-colors" > <span>Tiếp tục: Nhập Biến  |
| UI-0962 | 1350 | event-prop | onClick | {() => setWizardStep(2)} |
| UI-0963 | 1395 | control | input | <input type="number" step={50000000} value={newAmount} onChange={(e) => setNewAmount(Number(e.target.value))} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg text-xs font-mo |
| UI-0964 | 1399 | event-prop | onChange | {(e) => setNewAmount(Number(e.target.value))} |
| UI-0965 | 1411 | control | select | <select value={newTermMonths} onChange={(e) => setNewTermMonths(Number(e.target.value))} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg text-xs font-semibold" > <option val |
| UI-0966 | 1413 | event-prop | onChange | {(e) => setNewTermMonths(Number(e.target.value))} |
| UI-0967 | 1432 | control | textarea | <textarea rows={2} value={newPurpose} onChange={(e) => setNewPurpose(e.target.value)} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg text-xs" placeholder="Mô tả cụ thể mục  |
| UI-0968 | 1435 | event-prop | onChange | {(e) => setNewPurpose(e.target.value)} |
| UI-0969 | 1462 | control | select | <select value={disbursementMethod} onChange={(e) => setDisbursementMethod(e.target.value)} className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-xs font-semibold" > <option valu |
| UI-0970 | 1464 | event-prop | onChange | {(e) => setDisbursementMethod(e.target.value)} |
| UI-0971 | 1477 | control | input | <input type="text" value={contractInvoiceNumber} onChange={(e) => setContractInvoiceNumber(e.target.value)} className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-xs font-mono" p |
| UI-0972 | 1480 | event-prop | onChange | {(e) => setContractInvoiceNumber(e.target.value)} |
| UI-0973 | 1490 | control | input | <input type="text" value={beneficiaryAccount} onChange={(e) => setBeneficiaryAccount(e.target.value)} className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-xs font-mono font-bol |
| UI-0974 | 1493 | event-prop | onChange | {(e) => setBeneficiaryAccount(e.target.value)} |
| UI-0975 | 1502 | control | input | <input type="text" value={beneficiaryBank} onChange={(e) => setBeneficiaryBank(e.target.value)} className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-xs" /> </div> <div classNam |
| UI-0976 | 1505 | event-prop | onChange | {(e) => setBeneficiaryBank(e.target.value)} |
| UI-0977 | 1514 | control | input | <input type="text" value={collateralType} onChange={(e) => setCollateralType(e.target.value)} className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-xs" /> </div> </div> ) : sele |
| UI-0978 | 1517 | event-prop | onChange | {(e) => setCollateralType(e.target.value)} |
| UI-0979 | 1529 | control | input | <input type="number" step={100000000} value={requestedLimit} onChange={(e) => setRequestedLimit(Number(e.target.value))} className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-xs |
| UI-0980 | 1533 | event-prop | onChange | {(e) => setRequestedLimit(Number(e.target.value))} |
| UI-0981 | 1542 | control | select | <select value={riskAppraisalMode} onChange={(e) => setRiskAppraisalMode(e.target.value)} className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-xs font-semibold" > <option value= |
| UI-0982 | 1544 | event-prop | onChange | {(e) => setRiskAppraisalMode(e.target.value)} |
| UI-0983 | 1557 | control | input | <input type="number" step={100000000} value={collateralValue} onChange={(e) => setCollateralValue(Number(e.target.value))} className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text- |
| UI-0984 | 1561 | event-prop | onChange | {(e) => setCollateralValue(Number(e.target.value))} |
| UI-0985 | 1570 | control | input | <input type="number" step={100000000} value={annualRevenue} onChange={(e) => setAnnualRevenue(Number(e.target.value))} className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-xs f |
| UI-0986 | 1574 | event-prop | onChange | {(e) => setAnnualRevenue(Number(e.target.value))} |
| UI-0987 | 1583 | control | input | <input type="text" value={cicTargetScore} onChange={(e) => setCicTargetScore(e.target.value)} className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-xs" /> </div> </div> ) : ( /* |
| UI-0988 | 1586 | event-prop | onChange | {(e) => setCicTargetScore(e.target.value)} |
| UI-0989 | 1613 | control | button | <button type="button" onClick={handleResetCustomVariables} className="px-2.5 py-1 text-[11px] font-semibold text-slate-600 dark:text-slate-400 hover:text-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg cursor-pointer" > K |
| UI-0990 | 1615 | event-prop | onClick | {handleResetCustomVariables} |
| UI-0991 | 1620 | control | button | <button type="button" onClick={() => setShowAddVarRow(!showAddVarRow)} className="flex items-center gap-1 px-3 py-1 bg-emerald-50 hover:bg-emerald-100 text-emerald-800 dark:bg-emerald-950/60 dark:hover:bg-emerald-900 dark:text-emerald-300 r |
| UI-0992 | 1622 | event-prop | onClick | {() => setShowAddVarRow(!showAddVarRow)} |
| UI-0993 | 1638 | control | button | <button onClick={() => setShowAddVarRow(false)} className="text-slate-400 hover:text-slate-600 p-0.5" > <X className="w-4 h-4" />  |
| UI-0994 | 1639 | event-prop | onClick | {() => setShowAddVarRow(false)} |
| UI-0995 | 1651 | control | input | <input type="text" placeholder="VD: maxDiscountPct" value={newVarKey} onChange={(e) => setNewVarKey(e.target.value)} className="w-full px-2.5 py-1.5 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-xs |
| UI-0996 | 1655 | event-prop | onChange | {(e) => setNewVarKey(e.target.value)} |
| UI-0997 | 1664 | control | select | <select value={newVarType} onChange={(e) => setNewVarType(e.target.value as any)} className="w-full px-2.5 py-1.5 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-xs font-semibold" > <option value="st |
| UI-0998 | 1666 | event-prop | onChange | {(e) => setNewVarType(e.target.value as any)} |
| UI-0999 | 1680 | control | input | <input type="text" placeholder="VD: true / 25 / value" value={newVarVal} onChange={(e) => setNewVarVal(e.target.value)} className="w-full px-2.5 py-1.5 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text |
| UI-1000 | 1684 | event-prop | onChange | {(e) => setNewVarVal(e.target.value)} |
| UI-1001 | 1693 | control | input | <input type="text" placeholder="Mô tả ngắn..." value={newVarDesc} onChange={(e) => setNewVarDesc(e.target.value)} className="w-full px-2.5 py-1.5 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-xs" / |
| UI-1002 | 1697 | event-prop | onChange | {(e) => setNewVarDesc(e.target.value)} |
| UI-1003 | 1704 | control | button | <button type="button" onClick={() => setShowAddVarRow(false)} className="px-3 py-1 border border-slate-200 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer" > Hủy  |
| UI-1004 | 1706 | event-prop | onClick | {() => setShowAddVarRow(false)} |
| UI-1005 | 1711 | control | button | <button type="button" onClick={handleAddCustomVariable} className="px-3 py-1 bg-emerald-600 hover:bg-emerald-700 text-white rounded-lg text-xs font-semibold cursor-pointer shadow-xs" > Lưu Biến  |
| UI-1006 | 1713 | event-prop | onClick | {handleAddCustomVariable} |
| UI-1007 | 1746 | control | input | <input type="text" value={v.value} onChange={(e) => { const newVal = e.target.value; setCustomVariables(prev => prev.map((item, i) => i === idx ? { ...item, value: newVal } : item)); }} className="w-full px-2 py-1 bg-slate-50 dark:bg-slate- |
| UI-1008 | 1749 | event-prop | onChange | {(e) => { const newVal = e.target.value; setCustomVariables(prev => prev.map((item, i) => i === idx ? { ...item, value: newVal } : item)); }} |
| UI-1009 | 1760 | control | button | <button type="button" onClick={() => handleRemoveCustomVariable(v.key)} className="text-slate-400 hover:text-rose-600 p-1 cursor-pointer transition-colors" title="Xóa biến này" > <X className="w-3.5 h-3.5" />  |
| UI-1010 | 1762 | event-prop | onClick | {() => handleRemoveCustomVariable(v.key)} |
| UI-1011 | 1778 | control | button | <button onClick={() => setWizardStep(1)} className="flex items-center gap-1.5 px-4 py-2 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-800" > <ChevronLeft |
| UI-1012 | 1779 | event-prop | onClick | {() => setWizardStep(1)} |
| UI-1013 | 1786 | control | button | <button onClick={() => setWizardStep(3)} className="flex items-center gap-1.5 px-4 py-2 bg-emerald-600 hover:bg-emerald-700 text-white rounded-lg text-xs font-semibold cursor-pointer shadow-xs transition-colors" > <span>Tiếp tục: Đính Kèm H |
| UI-1014 | 1787 | event-prop | onClick | {() => setWizardStep(3)} |
| UI-1015 | 1885 | control | button | <button type="button" onClick={() => setUploadedFiles(prev => prev.filter(f => f !== attachedFile))} className="px-2.5 py-1 text-rose-600 hover:bg-rose-50 dark:hover:bg-rose-950/40 rounded-lg text-xs font-semibold cursor-pointer border bord |
| UI-1016 | 1887 | event-prop | onClick | {() => setUploadedFiles(prev => prev.filter(f => f !== attachedFile))} |
| UI-1017 | 1893 | control | button | <button type="button" onClick={() => { setUploadedFiles(prev => [ ...prev, { name: &#96;${reqDoc.key.toUpperCase()}_${newCustomerTaxId}.pdf&#96;, size: '2.4 MB', type: reqDoc.title, requiredKey: reqDoc.key } ]); showToast(&#96;Đã đính kèm t |
| UI-1018 | 1895 | event-prop | onClick | {() => { setUploadedFiles(prev => [ ...prev, { name: &#96;${reqDoc.key.toUpperCase()}_${newCustomerTaxId}.pdf&#96;, size: '2.4 MB', type: reqDoc.title, requiredKey: reqDoc.key } ]); showToast(&#96;Đã đính kèm tệp cho "${reqDoc.title}"!&#96; |
| UI-1019 | 1929 | control | button | <button type="button" onClick={() => { setUploadedFiles(prev => [ ...prev, { name: &#96;TaiLieu_BoSung_${Date.now().toString().slice(-4)}.pdf&#96;, size: '1.8 MB', type: 'Tài liệu bổ sung khác' } ]); showToast('Đã thêm 1 tệp đính kèm bổ sun |
| UI-1020 | 1931 | event-prop | onClick | {() => { setUploadedFiles(prev => [ ...prev, { name: &#96;TaiLieu_BoSung_${Date.now().toString().slice(-4)}.pdf&#96;, size: '1.8 MB', type: 'Tài liệu bổ sung khác' } ]); showToast('Đã thêm 1 tệp đính kèm bổ sung!'); }} |
| UI-1021 | 1964 | control | button | <button type="button" onClick={() => setUploadedFiles(prev => prev.filter((_, idx) => idx !== i))} className="text-slate-400 hover:text-rose-600 p-1 cursor-pointer" title="Xóa tệp này" > <X className="w-4 h-4" />  |
| UI-1022 | 1966 | event-prop | onClick | {() => setUploadedFiles(prev => prev.filter((_, idx) => idx !== i))} |
| UI-1023 | 1979 | control | button | <button onClick={() => setWizardStep(2)} className="flex items-center gap-1.5 px-4 py-2 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-800" > <ChevronLeft |
| UI-1024 | 1980 | event-prop | onClick | {() => setWizardStep(2)} |
| UI-1025 | 1987 | control | button | <button onClick={() => setWizardStep(4)} className="flex items-center gap-1.5 px-4 py-2 bg-emerald-600 hover:bg-emerald-700 text-white rounded-lg text-xs font-semibold cursor-pointer shadow-xs transition-colors" > <span>Tiếp tục: Rà Soát Sc |
| UI-1026 | 1988 | event-prop | onClick | {() => setWizardStep(4)} |
| UI-1027 | 2061 | control | button | <button type="button" onClick={() => setPayloadViewMode('json')} className={&#96;px-2.5 py-1 rounded-md transition-all cursor-pointer ${ payloadViewMode === 'json' ? 'bg-white dark:bg-slate-900 text-emerald-700 dark:text-emerald-400 shadow- |
| UI-1028 | 2063 | event-prop | onClick | {() => setPayloadViewMode('json')} |
| UI-1029 | 2070 | control | button | <button type="button" onClick={() => setPayloadViewMode('table')} className={&#96;px-2.5 py-1 rounded-md transition-all cursor-pointer ${ payloadViewMode === 'table' ? 'bg-white dark:bg-slate-900 text-emerald-700 dark:text-emerald-400 shado |
| UI-1030 | 2072 | event-prop | onClick | {() => setPayloadViewMode('table')} |
| UI-1031 | 2081 | control | button | <button type="button" onClick={() => { navigator.clipboard.writeText(JSON.stringify(buildEnginePayload(), null, 2)); setJsonCopied(true); setTimeout(() => setJsonCopied(false), 2000); showToast('Đã sao chép JSON Payload vào bộ nhớ đệm!'); } |
| UI-1032 | 2083 | event-prop | onClick | {() => { navigator.clipboard.writeText(JSON.stringify(buildEnginePayload(), null, 2)); setJsonCopied(true); setTimeout(() => setJsonCopied(false), 2000); showToast('Đã sao chép JSON Payload vào bộ nhớ đệm!'); }} |
| UI-1033 | 2157 | control | input | <input type="checkbox" id="esignCheck" checked={eSignAccepted} onChange={(e) => setESignAccepted(e.target.checked)} className="mt-1 rounded text-emerald-600 focus:ring-emerald-500 cursor-pointer" /> <label htmlFor="esignCheck" className="te |
| UI-1034 | 2161 | event-prop | onChange | {(e) => setESignAccepted(e.target.checked)} |
| UI-1035 | 2177 | control | button | <button onClick={() => setWizardStep(3)} className="flex items-center gap-1.5 px-4 py-2 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-800" > <ChevronLeft |
| UI-1036 | 2178 | event-prop | onClick | {() => setWizardStep(3)} |
| UI-1037 | 2185 | control | button | <button disabled={!eSignAccepted} onClick={handleSubmitNewRequest} className="flex items-center gap-2 px-6 py-2.5 bg-gradient-to-r from-emerald-600 to-teal-600 hover:from-emerald-700 hover:to-teal-700 disabled:opacity-50 text-white rounded- |
| UI-1038 | 2187 | event-prop | onClick | {handleSubmitNewRequest} |
| UI-1039 | 2213 | control | button | <button onClick={() => { setSidebarCollapsed(true); showToast('Đã thu gọn danh sách bên trái để mở rộng tối đa vùng xem sơ đồ quy trình!'); }} className="p-1 text-slate-400 hover:text-emerald-600 rounded cursor-pointer transition-colors" ti |
| UI-1040 | 2214 | event-prop | onClick | {() => { setSidebarCollapsed(true); showToast('Đã thu gọn danh sách bên trái để mở rộng tối đa vùng xem sơ đồ quy trình!'); }} |
| UI-1041 | 2226 | control | input | <input type="text" placeholder="Tìm theo mã hồ sơ, tên công ty..." value={searchTerm} onChange={(e) => setSearchTerm(e.target.value)} className="w-full pl-8 pr-3 py-1.5 text-xs bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:bord |
| UI-1042 | 2230 | event-prop | onChange | {(e) => setSearchTerm(e.target.value)} |
| UI-1043 | 2238 | control | button | <button key={st} onClick={() => setStatusFilter(st)} className={&#96;px-2 py-0.5 rounded-md text-[10.5px] font-semibold whitespace-nowrap cursor-pointer transition-all ${ statusFilter === st ? 'bg-emerald-600 text-white' : 'bg-slate-100 dar |
| UI-1044 | 2240 | event-prop | onClick | {() => setStatusFilter(st)} |
| UI-1045 | 2261 | event-prop | onClick | {() => setSelectedRequestId(req.id)} |
| UI-1046 | 2286 | control | button | <button type="button" onClick={(e) => { e.stopPropagation(); setSelectedRequestId(req.id); setRequestDetailTab('embedded-bpmn'); }} className="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-emerald-50 hover:bg-emerald-100 dark:bg-emeral |
| UI-1047 | 2288 | event-prop | onClick | {(e) => { e.stopPropagation(); setSelectedRequestId(req.id); setRequestDetailTab('embedded-bpmn'); }} |
| UI-1048 | 2356 | control | button | <button onClick={() => setSidebarCollapsed(false)} className="flex items-center gap-1 px-2.5 py-1 text-xs font-semibold text-emerald-700 dark:text-emerald-300 bg-emerald-50 dark:bg-emerald-950 border border-emerald-200 dark:border-emerald-8 |
| UI-1049 | 2357 | event-prop | onClick | {() => setSidebarCollapsed(false)} |
| UI-1050 | 2381 | control | button | <button onClick={() => { setSidebarCollapsed(!sidebarCollapsed); showToast(sidebarCollapsed ? 'Đã hiển thị lại danh sách hồ sơ' : 'Đã mở rộng vùng xem quy trình tối đa màn hình'); }} className="flex items-center gap-1.5 px-3 py-1.5 text-xs  |
| UI-1051 | 2382 | event-prop | onClick | {() => { setSidebarCollapsed(!sidebarCollapsed); showToast(sidebarCollapsed ? 'Đã hiển thị lại danh sách hồ sơ' : 'Đã mở rộng vùng xem quy trình tối đa màn hình'); }} |
| UI-1052 | 2407 | control | button | <button onClick={() => setRequestDetailTab('embedded-bpmn')} className={&#96;flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-semibold transition-all cursor-pointer whitespace-nowrap ${ requestDetailTab === 'embedded-bpmn' ? 'b |
| UI-1053 | 2408 | event-prop | onClick | {() => setRequestDetailTab('embedded-bpmn')} |
| UI-1054 | 2419 | control | button | <button onClick={() => setRequestDetailTab('documents')} className={&#96;flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-semibold transition-all cursor-pointer whitespace-nowrap ${ requestDetailTab === 'documents' ? 'bg-white  |
| UI-1055 | 2420 | event-prop | onClick | {() => setRequestDetailTab('documents')} |
| UI-1056 | 2431 | control | button | <button onClick={() => setRequestDetailTab('comments')} className={&#96;flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-semibold transition-all cursor-pointer whitespace-nowrap ${ requestDetailTab === 'comments' ? 'bg-white da |
| UI-1057 | 2432 | event-prop | onClick | {() => setRequestDetailTab('comments')} |
| UI-1058 | 2443 | control | button | <button onClick={() => setRequestDetailTab('stepper')} className={&#96;flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-semibold transition-all cursor-pointer whitespace-nowrap ${ requestDetailTab === 'stepper' ? 'bg-white dark |
| UI-1059 | 2444 | event-prop | onClick | {() => setRequestDetailTab('stepper')} |
| UI-1060 | 2471 | control | button | <button onClick={() => setShowAddDocsModal(true)} className="flex items-center gap-1.5 px-3.5 py-2 bg-rose-600 hover:bg-rose-700 text-white rounded-lg text-xs font-bold transition-all shadow-xs cursor-pointer" > <Upload className="w-3.5 h-3 |
| UI-1061 | 2472 | event-prop | onClick | {() => setShowAddDocsModal(true)} |
| UI-1062 | 2490 | event-prop | onNotify | {showToast} |
| UI-1063 | 2491 | event-prop | onOpenAdminConsole | {onOpenAdminConsole} |
| UI-1064 | 2492 | event-prop | onUploadAdditionalDoc | {() => setShowAddDocsModal(true)} |
| UI-1065 | 2503 | control | button | <button onClick={() => setShowAddDocsModal(true)} className="flex items-center gap-1 px-3 py-1 bg-emerald-50 hover:bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300 rounded-lg font-semibold cursor-pointer border bord |
| UI-1066 | 2504 | event-prop | onClick | {() => setShowAddDocsModal(true)} |
| UI-1067 | 2537 | control | button | <button onClick={() => showToast(&#96;Đang tải tệp "${doc.fileName}"...&#96;)} className="p-1.5 text-slate-400 hover:text-emerald-600 cursor-pointer rounded hover:bg-slate-100 dark:hover:bg-slate-800" title="Tải về" > <Download className="w |
| UI-1068 | 2538 | event-prop | onClick | {() => showToast(&#96;Đang tải tệp "${doc.fileName}"...&#96;)} |
| UI-1069 | 2590 | control | input | <input type="text" value={newCommentText} onChange={(e) => setNewCommentText(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && handleSendComment()} placeholder="Nhập nội dung trao đổi hoặc thắc mắc gửi cán bộ..." className="flex-1 px- |
| UI-1070 | 2593 | event-prop | onChange | {(e) => setNewCommentText(e.target.value)} |
| UI-1071 | 2594 | event-prop | onKeyDown | {(e) => e.key === 'Enter' && handleSendComment()} |
| UI-1072 | 2598 | control | button | <button onClick={handleSendComment} className="px-4 py-2 bg-emerald-600 hover:bg-emerald-700 text-white rounded-lg text-xs font-semibold cursor-pointer shadow-xs" > Gửi  |
| UI-1073 | 2599 | event-prop | onClick | {handleSendComment} |
| UI-1074 | 2740 | control | button | <button onClick={() => { setSelectedRequestId(req.id); showToast(&#96;Đã kiểm tra CIC cho khách hàng ${req.customerName}: Điểm tín dụng Hạng 1 (Good - 720/800)!&#96;); }} className="px-2 py-1 bg-slate-100 hover:bg-slate-200 dark:bg-slate-80 |
| UI-1075 | 2741 | event-prop | onClick | {() => { setSelectedRequestId(req.id); showToast(&#96;Đã kiểm tra CIC cho khách hàng ${req.customerName}: Điểm tín dụng Hạng 1 (Good - 720/800)!&#96;); }} |
| UI-1076 | 2751 | control | button | <button onClick={() => { setSelectedRequestId(req.id); setOfficerDecision('APPROVE'); setShowOfficerActionModal(true); }} className="px-2.5 py-1 bg-emerald-600 hover:bg-emerald-700 text-white rounded text-[10.5px] font-semibold cursor-point |
| UI-1077 | 2752 | event-prop | onClick | {() => { setSelectedRequestId(req.id); setOfficerDecision('APPROVE'); setShowOfficerActionModal(true); }} |
| UI-1078 | 2790 | control | input | <input type="text" value={lookupQuery} onChange={(e) => setLookupQuery(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && handlePerformLookup()} placeholder="Nhập mã hồ sơ (VD: HS-2026-0891)..." className="flex-1 px-3.5 py-2.5 text-xs  |
| UI-1079 | 2793 | event-prop | onChange | {(e) => setLookupQuery(e.target.value)} |
| UI-1080 | 2794 | event-prop | onKeyDown | {(e) => e.key === 'Enter' && handlePerformLookup()} |
| UI-1081 | 2798 | control | button | <button onClick={handlePerformLookup} className="px-5 py-2.5 bg-emerald-600 hover:bg-emerald-700 text-white rounded-xl text-xs font-bold cursor-pointer shadow-xs flex items-center gap-1.5" > <Search className="w-4 h-4" /> <span>Tra Cứu</spa |
| UI-1082 | 2799 | event-prop | onClick | {handlePerformLookup} |
| UI-1083 | 2841 | control | button | <button onClick={() => { setSelectedRequestId(lookupResult.id); setRequestDetailTab('embedded-bpmn'); setActiveTab('my-requests'); }} className="text-emerald-700 dark:text-emerald-400 font-semibold hover:underline flex items-center gap-1 cu |
| UI-1084 | 2842 | event-prop | onClick | {() => { setSelectedRequestId(lookupResult.id); setRequestDetailTab('embedded-bpmn'); setActiveTab('my-requests'); }} |
| UI-1085 | 2877 | event-prop | onNotify | {showToast} |
| UI-1086 | 2878 | event-prop | onOpenAdminConsole | {onOpenAdminConsole} |
| UI-1087 | 2879 | event-prop | onUploadAdditionalDoc | {() => { setSelectedRequestId(lookupResult.id); setShowAddDocsModal(true); }} |
| UI-1088 | 2921 | control | button | <button onClick={() => showToast(&#96;Đang tải xuống tệp biểu mẫu: ${form.title}...&#96;)} className="p-1.5 text-slate-500 hover:text-emerald-600 rounded cursor-pointer" title="Tải về biểu mẫu" > <Download className="w-4 h-4" />  |
| UI-1089 | 2922 | event-prop | onClick | {() => showToast(&#96;Đang tải xuống tệp biểu mẫu: ${form.title}...&#96;)} |
| UI-1090 | 2956 | control | button | <button onClick={() => setShowAddDocsModal(false)} className="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer" > ✕  |
| UI-1091 | 2957 | event-prop | onClick | {() => setShowAddDocsModal(false)} |
| UI-1092 | 2973 | control | input | <input type="text" value={addDocFileName} onChange={(e) => setAddDocFileName(e.target.value)} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg text-xs" /> </div> <div classNa |
| UI-1093 | 2976 | event-prop | onChange | {(e) => setAddDocFileName(e.target.value)} |
| UI-1094 | 2985 | control | textarea | <textarea rows={3} value={addDocNotes} onChange={(e) => setAddDocNotes(e.target.value)} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg text-xs" /> </div> </div> <div classN |
| UI-1095 | 2988 | event-prop | onChange | {(e) => setAddDocNotes(e.target.value)} |
| UI-1096 | 2995 | control | button | <button onClick={() => setShowAddDocsModal(false)} className="px-3.5 py-1.5 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer hover:bg-slate-100" > Hủy  |
| UI-1097 | 2996 | event-prop | onClick | {() => setShowAddDocsModal(false)} |
| UI-1098 | 3001 | control | button | <button onClick={handleClientAddDocs} className="px-4 py-1.5 bg-rose-600 hover:bg-rose-700 text-white rounded-lg text-xs font-semibold cursor-pointer shadow-xs" > Xác Nhận Nộp Bổ Sung  |
| UI-1099 | 3002 | event-prop | onClick | {handleClientAddDocs} |
| UI-1100 | 3032 | control | button | <button onClick={() => setShowOfficerActionModal(false)} className="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer" > ✕  |
| UI-1101 | 3033 | event-prop | onClick | {() => setShowOfficerActionModal(false)} |
| UI-1102 | 3046 | control | button | <button type="button" onClick={() => setOfficerDecision('APPROVE')} className={&#96;p-2.5 rounded-lg border text-center transition-all cursor-pointer ${ officerDecision === 'APPROVE' ? 'border-emerald-500 bg-emerald-50 dark:bg-emerald-950/5 |
| UI-1103 | 3048 | event-prop | onClick | {() => setOfficerDecision('APPROVE')} |
| UI-1104 | 3059 | control | button | <button type="button" onClick={() => setOfficerDecision('REQUEST_DOCS')} className={&#96;p-2.5 rounded-lg border text-center transition-all cursor-pointer ${ officerDecision === 'REQUEST_DOCS' ? 'border-amber-500 bg-amber-50 dark:bg-amber-9 |
| UI-1105 | 3061 | event-prop | onClick | {() => setOfficerDecision('REQUEST_DOCS')} |
| UI-1106 | 3072 | control | button | <button type="button" onClick={() => setOfficerDecision('REJECT')} className={&#96;p-2.5 rounded-lg border text-center transition-all cursor-pointer ${ officerDecision === 'REJECT' ? 'border-rose-500 bg-rose-50 dark:bg-rose-950/50 text-rose |
| UI-1107 | 3074 | event-prop | onClick | {() => setOfficerDecision('REJECT')} |
| UI-1108 | 3091 | control | textarea | <textarea rows={3} value={officerReason} onChange={(e) => setOfficerReason(e.target.value)} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg text-xs" /> </div> </div> <div cl |
| UI-1109 | 3094 | event-prop | onChange | {(e) => setOfficerReason(e.target.value)} |
| UI-1110 | 3101 | control | button | <button onClick={() => setShowOfficerActionModal(false)} className="px-3.5 py-1.5 border border-slate-300 dark:border-slate-700 rounded-lg text-xs font-semibold cursor-pointer hover:bg-slate-100" > Hủy  |
| UI-1111 | 3102 | event-prop | onClick | {() => setShowOfficerActionModal(false)} |
| UI-1112 | 3107 | control | button | <button onClick={handleOfficerDecisionSubmit} className="px-4 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-xs font-semibold cursor-pointer shadow-xs" > Xác Nhận Quyết Định  |
| UI-1113 | 3108 | event-prop | onClick | {handleOfficerDecisionSubmit} |

## components/portal/EmbeddedProcessViewer.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-1114 | 440 | control | button | <button onClick={onOpenAdminConsole} className="flex items-center gap-1.5 px-2.5 py-1 text-xs font-semibold text-slate-700 dark:text-slate-300 hover:text-emerald-700 bg-slate-100 dark:bg-slate-800 rounded-lg border border-slate-200 dark:bor |
| UI-1115 | 441 | event-prop | onClick | {onOpenAdminConsole} |
| UI-1116 | 450 | control | button | <button onClick={() => setIsFullscreen(!isFullscreen)} className="p-1.5 text-slate-500 hover:text-slate-800 dark:hover:text-slate-200 bg-slate-100 dark:bg-slate-800 rounded-lg cursor-pointer transition-colors" title={isFullscreen ? 'Thu nhỏ |
| UI-1117 | 451 | event-prop | onClick | {() => setIsFullscreen(!isFullscreen)} |
| UI-1118 | 466 | control | button | <button onClick={() => copyToClipboard(processInstanceKey, 'Instance Key')} className="text-slate-400 hover:text-emerald-600 cursor-pointer" title="Sao chép Instance Key" > {copiedKey === 'Instance Key' ? <Check className="w-3 h-3 text-emer |
| UI-1119 | 467 | event-prop | onClick | {() => copyToClipboard(processInstanceKey, 'Instance Key')} |
| UI-1120 | 480 | control | button | <button onClick={() => copyToClipboard(businessKey, 'Business Key')} className="text-slate-400 hover:text-emerald-600 cursor-pointer" title="Sao chép Business Key" > {copiedKey === 'Business Key' ? <Check className="w-3 h-3 text-emerald-500 |
| UI-1121 | 481 | event-prop | onClick | {() => copyToClipboard(businessKey, 'Business Key')} |
| UI-1122 | 512 | control | button | <button onClick={() => setActiveTab('diagram')} className={&#96;flex items-center gap-2 px-3.5 py-2.5 font-semibold border-b-2 transition-all cursor-pointer ${ activeTab === 'diagram' ? 'border-emerald-600 text-emerald-700 dark:text-emerald |
| UI-1123 | 513 | event-prop | onClick | {() => setActiveTab('diagram')} |
| UI-1124 | 524 | control | button | <button onClick={() => setActiveTab('variables')} className={&#96;flex items-center gap-2 px-3.5 py-2.5 font-semibold border-b-2 transition-all cursor-pointer ${ activeTab === 'variables' ? 'border-emerald-600 text-emerald-700 dark:text-eme |
| UI-1125 | 525 | event-prop | onClick | {() => setActiveTab('variables')} |
| UI-1126 | 541 | control | button | <button onClick={() => { setFitMode('fit'); setZoomLevel(1); showToast('Đã căn chỉnh luồng quy trình vừa khít khung hình, loại bỏ khoảng trắng dư thừa!'); }} className={&#96;px-2 py-0.5 rounded font-semibold transition-all cursor-pointer ${ |
| UI-1127 | 542 | event-prop | onClick | {() => { setFitMode('fit'); setZoomLevel(1); showToast('Đã căn chỉnh luồng quy trình vừa khít khung hình, loại bỏ khoảng trắng dư thừa!'); }} |
| UI-1128 | 556 | control | button | <button onClick={() => { setFitMode('manual'); showToast('Chuyển sang chế độ cuộn tự do và phóng to / thu nhỏ.'); }} className={&#96;px-2 py-0.5 rounded font-semibold transition-all cursor-pointer ${ fitMode === 'manual' ? 'bg-white dark:bg |
| UI-1129 | 557 | event-prop | onClick | {() => { setFitMode('manual'); showToast('Chuyển sang chế độ cuộn tự do và phóng to / thu nhỏ.'); }} |
| UI-1130 | 573 | control | button | <button onClick={() => { setSelectedNodeId(activeNode.id); setShowInspector(true); showToast(&#96;Đã tập trung vào bước đang chạy: ${activeNode.name}&#96;); }} className="flex items-center gap-1 px-2 py-1 text-[10.5px] font-semibold text-em |
| UI-1131 | 574 | event-prop | onClick | {() => { setSelectedNodeId(activeNode.id); setShowInspector(true); showToast(&#96;Đã tập trung vào bước đang chạy: ${activeNode.name}&#96;); }} |
| UI-1132 | 587 | control | button | <button onClick={() => { setShowInspector(!showInspector); showToast(!showInspector ? 'Đã mở bảng thông tin chi tiết bước quy trình' : 'Đã thu gọn bảng chi tiết để mở rộng tối đa sơ đồ quy trình'); }} className={&#96;flex items-center gap-1 |
| UI-1133 | 588 | event-prop | onClick | {() => { setShowInspector(!showInspector); showToast(!showInspector ? 'Đã mở bảng thông tin chi tiết bước quy trình' : 'Đã thu gọn bảng chi tiết để mở rộng tối đa sơ đồ quy trình'); }} |
| UI-1134 | 606 | control | button | <button onClick={() => setZoomLevel(prev => Math.max(0.5, prev - 0.15))} className="p-1 text-slate-500 hover:text-slate-800 dark:hover:text-slate-200 rounded border border-slate-200 dark:border-slate-700 cursor-pointer" title="Thu nhỏ sơ đồ |
| UI-1135 | 607 | event-prop | onClick | {() => setZoomLevel(prev => Math.max(0.5, prev - 0.15))} |
| UI-1136 | 616 | control | button | <button onClick={() => setZoomLevel(prev => Math.min(1.8, prev + 0.15))} className="p-1 text-slate-500 hover:text-slate-800 dark:hover:text-slate-200 rounded border border-slate-200 dark:border-slate-700 cursor-pointer" title="Phóng to sơ đ |
| UI-1137 | 617 | event-prop | onClick | {() => setZoomLevel(prev => Math.min(1.8, prev + 0.15))} |
| UI-1138 | 623 | control | button | <button onClick={() => setZoomLevel(1)} className="p-1 text-slate-500 hover:text-slate-800 dark:hover:text-slate-200 rounded border border-slate-200 dark:border-slate-700 cursor-pointer" title="Đặt lại tỉ lệ 1:1" > <RotateCcw className="w-3 |
| UI-1139 | 624 | event-prop | onClick | {() => setZoomLevel(1)} |
| UI-1140 | 648 | control | button | <button onClick={onUploadAdditionalDoc} className="px-3 py-1 bg-rose-600 hover:bg-rose-700 text-white rounded-lg text-xs font-bold cursor-pointer transition-colors shadow-xs" > Bổ Sung Tài Liệu Ngay  |
| UI-1141 | 649 | event-prop | onClick | {onUploadAdditionalDoc} |
| UI-1142 | 814 | event-prop | onClick | {() => { setSelectedNodeId(node.id); setShowInspector(true); }} |
| UI-1143 | 1012 | control | button | <button onClick={() => setShowInspector(false)} className="p-1 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 rounded cursor-pointer transition-colors" title="Thu gọn bảng chi tiết để mở rộng sơ đồ" > <X className="w-3.5 h-3. |
| UI-1144 | 1013 | event-prop | onClick | {() => setShowInspector(false)} |
| UI-1145 | 1072 | control | button | <button onClick={() => setActiveTab('variables')} className="w-full py-2 bg-slate-100 hover:bg-slate-200 dark:bg-slate-800 dark:hover:bg-slate-700 text-slate-800 dark:text-slate-200 rounded-xl text-xs font-semibold cursor-pointer transition |
| UI-1146 | 1073 | event-prop | onClick | {() => setActiveTab('variables')} |
| UI-1147 | 1105 | control | button | <button onClick={() => setVarViewMode('table')} className={&#96;px-3 py-1 rounded-md transition-all cursor-pointer ${ varViewMode === 'table' ? 'bg-white dark:bg-slate-900 text-emerald-700 dark:text-emerald-400 shadow-2xs' : 'text-slate-600 |
| UI-1148 | 1106 | event-prop | onClick | {() => setVarViewMode('table')} |
| UI-1149 | 1113 | control | button | <button onClick={() => setVarViewMode('json')} className={&#96;px-3 py-1 rounded-md transition-all cursor-pointer ${ varViewMode === 'json' ? 'bg-white dark:bg-slate-900 text-emerald-700 dark:text-emerald-400 shadow-2xs' : 'text-slate-600 d |
| UI-1150 | 1114 | event-prop | onClick | {() => setVarViewMode('json')} |
| UI-1151 | 1123 | control | button | <button onClick={() => { const payloadObj: Record<string, any> = {}; consolidatedVariables.forEach(v => { payloadObj[v.key] = v.value; }); navigator.clipboard.writeText(JSON.stringify(payloadObj, null, 2)); setCopiedJson(true); setTimeout(( |
| UI-1152 | 1124 | event-prop | onClick | {() => { const payloadObj: Record<string, any> = {}; consolidatedVariables.forEach(v => { payloadObj[v.key] = v.value; }); navigator.clipboard.writeText(JSON.stringify(payloadObj, null, 2)); setCopiedJson(true); setTimeout(() => setCopiedJs |
| UI-1153 | 1144 | control | input | <input type="text" placeholder="Tìm kiếm biến theo tên key, giá trị hoặc mô tả..." value={varSearchQuery} onChange={(e) => setVarSearchQuery(e.target.value)} className="w-full pl-9 pr-3 py-1.5 text-xs bg-slate-50 dark:bg-slate-800 border bo |
| UI-1154 | 1148 | event-prop | onChange | {(e) => setVarSearchQuery(e.target.value)} |
| UI-1155 | 1162 | control | button | <button key={cat.id} onClick={() => setVarCategory(cat.id as any)} className={&#96;px-2.5 py-1 rounded-md text-[11px] font-semibold transition-all cursor-pointer whitespace-nowrap ${ varCategory === cat.id ? 'bg-emerald-600 text-white shado |
| UI-1156 | 1164 | event-prop | onClick | {() => setVarCategory(cat.id as any)} |
| UI-1157 | 1216 | control | button | <button onClick={() => copyToClipboard(String(v.value), v.key)} className="p-1 text-slate-400 hover:text-emerald-600 cursor-pointer" title="Sao chép giá trị" > {copiedKey === v.key ? <Check className="w-3.5 h-3.5 text-emerald-500" /> : <Cop |
| UI-1158 | 1217 | event-prop | onClick | {() => copyToClipboard(String(v.value), v.key)} |

## components/portal/PortalMetricsDashboard.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-1159 | 307 | control | select | <select value={selectedProcessFilter} onChange={(e) => setSelectedProcessFilter(e.target.value)} className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg px-2.5 py-1 text-xs font-semibold text-slate-800 |
| UI-1160 | 309 | event-prop | onChange | {(e) => setSelectedProcessFilter(e.target.value)} |
| UI-1161 | 323 | control | button | <button key={tr} onClick={() => setTimeRange(tr)} className={&#96;px-3 py-1 rounded-lg text-xs font-semibold transition-all cursor-pointer ${ timeRange === tr ? 'bg-white dark:bg-slate-900 text-emerald-700 dark:text-emerald-400 shadow-2xs f |
| UI-1162 | 325 | event-prop | onClick | {() => setTimeRange(tr)} |
| UI-1163 | 338 | control | button | <button onClick={handleRefresh} className="p-2 text-slate-600 dark:text-slate-300 hover:text-emerald-600 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 rounded-xl border border-slate-200 dark:border-slate-700 tran |
| UI-1164 | 339 | event-prop | onClick | {handleRefresh} |
| UI-1165 | 354 | event-prop | onClick | {() => { if (onFilterStatus) onFilterStatus('IN_REVIEW'); onNavigateTab('my-requests'); }} |
| UI-1166 | 412 | event-prop | onClick | {() => onNavigateTab('officer-desk')} |
| UI-1167 | 728 | control | button | <button onClick={() => onNavigateTab('officer-desk')} className="text-xs font-semibold text-purple-600 hover:underline flex items-center gap-1 cursor-pointer" > <span>Xử lý ngay</span> <ArrowRight className="w-3.5 h-3.5" />  |
| UI-1168 | 729 | event-prop | onClick | {() => onNavigateTab('officer-desk')} |

## components/task-management/AccessAuditLog.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-1169 | 238 | control | input | <input type="text" placeholder="Tìm kiếm Actor, Resource, Action hoặc Chi tiết..." value={searchTerm} onChange={(e) => { setSearchTerm(e.target.value); setCurrentPage(1); }} className="w-full pl-8 pr-8 py-1.5 text-xs bg-slate-50 dark:bg-sla |
| UI-1170 | 242 | event-prop | onChange | {(e) => { setSearchTerm(e.target.value); setCurrentPage(1); }} |
| UI-1171 | 250 | control | button | <button onClick={() => setSearchTerm('')} className="absolute right-2.5 top-2 text-slate-400 hover:text-slate-600 text-xs cursor-pointer" > ✕  |
| UI-1172 | 251 | event-prop | onClick | {() => setSearchTerm('')} |
| UI-1173 | 264 | control | select | <select value={filterResource} onChange={(e) => { setFilterResource(e.target.value); setCurrentPage(1); }} className="px-2 py-1 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-md text-slate-700 dark:text- |
| UI-1174 | 266 | event-prop | onChange | {(e) => { setFilterResource(e.target.value); setCurrentPage(1); }} |
| UI-1175 | 281 | control | select | <select value={filterAction} onChange={(e) => { setFilterAction(e.target.value); setCurrentPage(1); }} className="px-2 py-1 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-md text-slate-700 dark:text-slat |
| UI-1176 | 283 | event-prop | onChange | {(e) => { setFilterAction(e.target.value); setCurrentPage(1); }} |
| UI-1177 | 301 | control | select | <select value={filterProcess} onChange={(e) => { setFilterProcess(e.target.value); setCurrentPage(1); }} className="px-2 py-1 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-md text-slate-700 dark:text-sl |
| UI-1178 | 303 | event-prop | onChange | {(e) => { setFilterProcess(e.target.value); setCurrentPage(1); }} |
| UI-1179 | 318 | control | button | <button onClick={() => { setSearchTerm(''); setFilterResource('ALL'); setFilterAction('ALL'); setFilterProcess('ALL'); setCurrentPage(1); }} className="text-xs text-emerald-700 dark:text-emerald-400 hover:underline font-semibold cursor-poin |
| UI-1180 | 319 | event-prop | onClick | {() => { setSearchTerm(''); setFilterResource('ALL'); setFilterAction('ALL'); setFilterProcess('ALL'); setCurrentPage(1); }} |
| UI-1181 | 332 | control | button | <button onClick={handleExportCSV} className="flex items-center gap-1.5 px-3 py-1 bg-emerald-700 hover:bg-emerald-800 text-white rounded-md text-xs font-semibold shadow-xs transition-colors cursor-pointer" title="Export audit log to CSV" > < |
| UI-1182 | 333 | event-prop | onClick | {handleExportCSV} |
| UI-1183 | 367 | control | button | <button onClick={() => { setSearchTerm(''); setFilterResource('ALL'); setFilterAction('ALL'); setFilterProcess('ALL'); setCurrentPage(1); }} className="mt-2 px-3 py-1 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 rounded text-xs font-se |
| UI-1184 | 368 | event-prop | onClick | {() => { setSearchTerm(''); setFilterResource('ALL'); setFilterAction('ALL'); setFilterProcess('ALL'); setCurrentPage(1); }} |
| UI-1185 | 477 | control | button | <button onClick={() => setCurrentPage((p) => Math.max(p - 1, 1))} disabled={currentPage === 1} className="px-2.5 py-1 rounded border border-slate-200 dark:border-slate-700 disabled:opacity-40 disabled:cursor-not-allowed hover:bg-slate-100 d |
| UI-1186 | 478 | event-prop | onClick | {() => setCurrentPage((p) => Math.max(p - 1, 1))} |
| UI-1187 | 487 | control | button | <button onClick={() => setCurrentPage((p) => Math.min(p + 1, totalPages))} disabled={currentPage === totalPages} className="px-2.5 py-1 rounded border border-slate-200 dark:border-slate-700 disabled:opacity-40 disabled:cursor-not-allowed ho |
| UI-1188 | 488 | event-prop | onClick | {() => setCurrentPage((p) => Math.min(p + 1, totalPages))} |

## components/task-management/TaskManagementModule.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-1189 | 346 | control | input | <input type="text" placeholder="Search process..." value={searchProcess} onChange={(e) => setSearchProcess(e.target.value)} className="w-full pl-8 pr-3 py-1.5 text-xs bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700  |
| UI-1190 | 350 | event-prop | onChange | {(e) => setSearchProcess(e.target.value)} |
| UI-1191 | 366 | control | button | <button key={proc.key} onClick={() => setSelectedProcessKey(proc.key)} className={&#96;w-full flex items-center justify-between px-3 py-2 rounded-lg text-xs font-semibold transition-all cursor-pointer ${ isSelected ? 'bg-emerald-100/70 dark |
| UI-1192 | 368 | event-prop | onClick | {() => setSelectedProcessKey(proc.key)} |
| UI-1193 | 431 | control | button | <button onClick={() => setViewMode('table')} className={&#96;flex items-center gap-1.5 px-3 py-1.5 rounded-lg transition-all cursor-pointer ${ viewMode === 'table' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 shadow-xs'  |
| UI-1194 | 432 | event-prop | onClick | {() => setViewMode('table')} |
| UI-1195 | 442 | control | button | <button onClick={() => setViewMode('matrix')} className={&#96;flex items-center gap-1.5 px-3 py-1.5 rounded-lg transition-all cursor-pointer ${ viewMode === 'matrix' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 shadow-xs |
| UI-1196 | 443 | event-prop | onClick | {() => setViewMode('matrix')} |
| UI-1197 | 458 | control | button | <button onClick={handleExportAuditCSV} className="flex items-center gap-1.5 px-3.5 py-1.5 bg-emerald-600 hover:bg-emerald-500 text-white rounded-lg text-xs font-semibold shadow-xs active:scale-[0.98] transition-all cursor-pointer" title="Tả |
| UI-1198 | 459 | event-prop | onClick | {handleExportAuditCSV} |
| UI-1199 | 470 | control | button | <button onClick={() => setShowCreateQueueModal(true)} className="flex items-center gap-1.5 px-3.5 py-1.5 bg-emerald-600 hover:bg-emerald-500 text-white rounded-lg text-xs font-semibold shadow-xs active:scale-[0.98] transition-all cursor-poi |
| UI-1200 | 471 | event-prop | onClick | {() => setShowCreateQueueModal(true)} |
| UI-1201 | 483 | control | button | <button onClick={() => setActiveScopeTab('process')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer flex items-center gap-1.5 ${ activeScopeTab === 'process' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate |
| UI-1202 | 484 | event-prop | onClick | {() => setActiveScopeTab('process')} |
| UI-1203 | 498 | control | button | <button onClick={() => setActiveScopeTab('element')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer flex items-center gap-1.5 ${ activeScopeTab === 'element' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate |
| UI-1204 | 499 | event-prop | onClick | {() => setActiveScopeTab('element')} |
| UI-1205 | 513 | control | button | <button onClick={() => setActiveScopeTab('queue')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer flex items-center gap-1.5 ${ activeScopeTab === 'queue' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 |
| UI-1206 | 514 | event-prop | onClick | {() => setActiveScopeTab('queue')} |
| UI-1207 | 528 | control | button | <button onClick={() => setActiveScopeTab('audit')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer flex items-center gap-1.5 ${ activeScopeTab === 'audit' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 |
| UI-1208 | 529 | event-prop | onClick | {() => setActiveScopeTab('audit')} |
| UI-1209 | 593 | control | button | <button onClick={() => { setSelectedProcessKey(proc.processKey); setActiveScopeTab('element'); }} className="px-2.5 py-1 bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300 hover:bg-emerald-100 rounded text-xs font-s |
| UI-1210 | 594 | event-prop | onClick | {() => { setSelectedProcessKey(proc.processKey); setActiveScopeTab('element'); }} |
| UI-1211 | 637 | control | button | <button key={act} onClick={() => handleToggleProcessAction(proc.processKey, role, act)} className={&#96;w-5 h-5 rounded text-[9px] font-bold transition-all cursor-pointer ${ has ? 'bg-emerald-600 text-white shadow-2xs' : 'bg-slate-100 dark: |
| UI-1212 | 639 | event-prop | onClick | {() => handleToggleProcessAction(proc.processKey, role, act)} |
| UI-1213 | 685 | event-prop | onClick | {() => handleOpenDrawer(item)} |
| UI-1214 | 744 | control | button | <button onClick={(e) => { e.stopPropagation(); handleOpenDrawer(item); }} className="p-1.5 text-slate-400 hover:text-emerald-600 dark:hover:text-emerald-400 hover:bg-slate-100 dark:hover:bg-slate-800 rounded transition-colors cursor-pointer |
| UI-1215 | 745 | event-prop | onClick | {(e) => { e.stopPropagation(); handleOpenDrawer(item); }} |
| UI-1216 | 791 | control | button | <button onClick={() => handleOpenDrawer(item)} className="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded bg-emerald-50 dark:bg-emerald-950/40 text-emerald-800 dark:text-emerald-300 font-mono text-[10px] font-bold border border-emera |
| UI-1217 | 792 | event-prop | onClick | {() => handleOpenDrawer(item)} |
| UI-1218 | 798 | control | button | <button onClick={() => handleOpenDrawer(item)} className="w-5 h-5 rounded hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-300 text-xs inline-flex items-center justify-center cursor-pointer" title={&#96;Gán quyền cho vai trò ${role}&#9 |
| UI-1219 | 799 | event-prop | onClick | {() => handleOpenDrawer(item)} |
| UI-1220 | 864 | control | button | <button onClick={() => handleOpenDrawer(queue)} className="p-1.5 text-slate-400 hover:text-emerald-600 rounded cursor-pointer" title="Cấu hình quyền trên queue này" > <Settings className="w-4 h-4" />  |
| UI-1221 | 865 | event-prop | onClick | {() => handleOpenDrawer(queue)} |
| UI-1222 | 883 | event-prop | onExportCSV | {handleExportAuditCSV} |
| UI-1223 | 894 | event-prop | onClick | {() => setDrawerOpen(false)} |
| UI-1224 | 913 | control | button | <button onClick={() => setDrawerOpen(false)} className="text-slate-400 hover:text-slate-600 p-1.5 rounded hover:bg-slate-200 dark:hover:bg-slate-700" > <X className="w-4 h-4" />  |
| UI-1225 | 914 | event-prop | onClick | {() => setDrawerOpen(false)} |
| UI-1226 | 931 | control | input | <input type="text" placeholder="Lọc vai trò..." value={roleSearchTerm} onChange={(e) => setRoleSearchTerm(e.target.value)} className="w-full pl-8 pr-3 py-1.5 text-xs bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-70 |
| UI-1227 | 935 | event-prop | onChange | {(e) => setRoleSearchTerm(e.target.value)} |
| UI-1228 | 946 | control | button | <button key={role} type="button" onClick={() => handleToggleRoleInDrawer(role)} className={&#96;px-2.5 py-1 rounded-md text-[11px] font-mono font-semibold transition-all flex items-center gap-1.5 cursor-pointer ${ isSelected ? 'bg-emerald-6 |
| UI-1229 | 949 | event-prop | onClick | {() => handleToggleRoleInDrawer(role)} |
| UI-1230 | 983 | control | button | <button onClick={() => handleToggleRoleInDrawer(role)} className="text-red-500 hover:text-red-700 text-[10.5px] cursor-pointer" > Xóa vai trò  |
| UI-1231 | 984 | event-prop | onClick | {() => handleToggleRoleInDrawer(role)} |
| UI-1232 | 994 | control | input | <input type="checkbox" checked={acts.includes('VIEW')} onChange={() => handleToggleGranularAction(role, 'VIEW')} className="rounded text-emerald-600 focus:ring-emerald-500" /> <span className="font-medium text-slate-700 dark:text-slate-300" |
| UI-1233 | 997 | event-prop | onChange | {() => handleToggleGranularAction(role, 'VIEW')} |
| UI-1234 | 1004 | control | input | <input type="checkbox" checked={acts.includes('EXECUTE')} onChange={() => handleToggleGranularAction(role, 'EXECUTE')} className="rounded text-emerald-600 focus:ring-emerald-500" /> <span className="font-medium text-slate-700 dark:text-slat |
| UI-1235 | 1007 | event-prop | onChange | {() => handleToggleGranularAction(role, 'EXECUTE')} |
| UI-1236 | 1014 | control | input | <input type="checkbox" checked={acts.includes('REASSIGN')} onChange={() => handleToggleGranularAction(role, 'REASSIGN')} className="rounded text-emerald-600 focus:ring-emerald-500" /> <span className="font-medium text-slate-700 dark:text-sl |
| UI-1237 | 1017 | event-prop | onChange | {() => handleToggleGranularAction(role, 'REASSIGN')} |
| UI-1238 | 1024 | control | input | <input type="checkbox" checked={acts.includes('CANCEL')} onChange={() => handleToggleGranularAction(role, 'CANCEL')} className="rounded text-emerald-600 focus:ring-emerald-500" /> <span className="font-medium text-slate-700 dark:text-slate- |
| UI-1239 | 1027 | event-prop | onChange | {() => handleToggleGranularAction(role, 'CANCEL')} |
| UI-1240 | 1055 | control | input | <input type="text" value={activeEditingItem.filterRules?.[0]?.expression &#124;&#124; 'loanAmount <= 15000000000 VND'} onChange={(e) => { const updatedRules = [{ id: 'f_dynamic', field: 'loanAmount', operator: '<=' as const, value: '1500000 |
| UI-1241 | 1058 | event-prop | onChange | {(e) => { const updatedRules = [{ id: 'f_dynamic', field: 'loanAmount', operator: '<=' as const, value: '15000000000', expression: e.target.value }]; setActiveEditingItem({ ...activeEditingItem, filters: e.target.value ? 1 : 0, filterRules: |
| UI-1242 | 1076 | event-prop | onClick | {() => setActiveEditingItem({ ...activeEditingItem, filters: 1, filterRules: [{ id: '1', field: 'loanAmount', operator: '<=', value: '5000000000', expression: 'loanAmount < 5,000,000,000 VND' }] })} |
| UI-1243 | 1080 | event-prop | onClick | {() => setActiveEditingItem({ ...activeEditingItem, filters: 1, filterRules: [{ id: '2', field: 'branch', operator: '==', value: 'HN', expression: 'branch == "Chi nhánh Hà Nội"' }] })} |
| UI-1244 | 1090 | control | button | <button onClick={() => setDrawerOpen(false)} className="px-4 py-2 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-700 dark:text-slate-300 font-semibold" > Hủy Bỏ  |
| UI-1245 | 1091 | event-prop | onClick | {() => setDrawerOpen(false)} |
| UI-1246 | 1096 | control | button | <button onClick={handleSaveDrawer} className="px-5 py-2 bg-emerald-700 hover:bg-emerald-800 text-white font-bold rounded-lg shadow-sm" > Lưu Cấu Hình RBAC  |
| UI-1247 | 1097 | event-prop | onClick | {handleSaveDrawer} |
| UI-1248 | 1117 | control | button | <button onClick={() => setShowCreateQueueModal(false)} className="text-slate-400 hover:text-slate-600">✕ |
| UI-1249 | 1117 | event-prop | onClick | {() => setShowCreateQueueModal(false)} |
| UI-1250 | 1122 | control | input | <input type="text" value={newQueueName} onChange={(e) => setNewQueueName(e.target.value)} placeholder="e.g. Hàng đợi hồ sơ VIP SME" className="w-full px-2.5 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rou |
| UI-1251 | 1125 | event-prop | onChange | {(e) => setNewQueueName(e.target.value)} |
| UI-1252 | 1132 | control | input | <input type="text" value={newQueueQuery} onChange={(e) => setNewQueueQuery(e.target.value)} placeholder="amount >= 5000000000 && branch == 'HN'" className="w-full px-2.5 py-1.5 bg-white dark:bg-slate-800 border border-slate-200 dark:border- |
| UI-1253 | 1135 | event-prop | onChange | {(e) => setNewQueueQuery(e.target.value)} |
| UI-1254 | 1144 | control | button | <button key={role} type="button" onClick={() => { setNewQueueRoles(prev => prev.includes(role) ? prev.filter(r => r !== role) : [...prev, role]); }} className={&#96;px-2 py-1 rounded text-[11px] font-mono cursor-pointer ${ newQueueRoles.inc |
| UI-1255 | 1147 | event-prop | onClick | {() => { setNewQueueRoles(prev => prev.includes(role) ? prev.filter(r => r !== role) : [...prev, role]); }} |
| UI-1256 | 1163 | control | button | <button onClick={() => setShowCreateQueueModal(false)} className="px-3 py-1.5 border border-slate-300 rounded text-xs font-semibold"> Hủy  |
| UI-1257 | 1163 | event-prop | onClick | {() => setShowCreateQueueModal(false)} |
| UI-1258 | 1166 | control | button | <button onClick={handleCreateQueue} className="px-4 py-1.5 bg-emerald-700 text-white rounded text-xs font-semibold hover:bg-emerald-800"> Tạo Queue  |
| UI-1259 | 1166 | event-prop | onClick | {handleCreateQueue} |

## components/tasklist/TasklistModule.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
| UI-1260 | 251 | control | button | <button onClick={() => setActiveTab('my')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer text-xs ${ activeTab === 'my' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 font-bold shadow-xs' : 'text-slat |
| UI-1261 | 252 | event-prop | onClick | {() => setActiveTab('my')} |
| UI-1262 | 261 | control | button | <button onClick={() => setActiveTab('group')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer text-xs ${ activeTab === 'group' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 font-bold shadow-xs' : 'tex |
| UI-1263 | 262 | event-prop | onClick | {() => setActiveTab('group')} |
| UI-1264 | 271 | control | button | <button onClick={() => setActiveTab('completed')} className={&#96;px-3 py-1.5 rounded-lg transition-all cursor-pointer text-xs ${ activeTab === 'completed' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 font-bold shadow-xs |
| UI-1265 | 272 | event-prop | onClick | {() => setActiveTab('completed')} |
| UI-1266 | 285 | control | button | <button onClick={() => setViewLayout('grid')} className={&#96;flex items-center gap-1.5 px-3 py-1.5 rounded-lg transition-all cursor-pointer ${ viewLayout === 'grid' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 shadow-xs |
| UI-1267 | 286 | event-prop | onClick | {() => setViewLayout('grid')} |
| UI-1268 | 297 | control | button | <button onClick={() => setViewLayout('split')} className={&#96;flex items-center gap-1.5 px-3 py-1.5 rounded-lg transition-all cursor-pointer ${ viewLayout === 'split' ? 'bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 shadow- |
| UI-1269 | 298 | event-prop | onClick | {() => setViewLayout('split')} |
| UI-1270 | 322 | control | input | <input type="text" placeholder="Tìm kiếm tác vụ, mã hồ sơ, tên khách hàng..." value={searchTerm} onChange={(e) => setSearchTerm(e.target.value)} className="w-full pl-8 pr-8 py-1.5 text-xs bg-slate-50 dark:bg-slate-800/60 border border-slate |
| UI-1271 | 326 | event-prop | onChange | {(e) => setSearchTerm(e.target.value)} |
| UI-1272 | 331 | control | button | <button onClick={() => setSearchTerm('')} className="absolute right-2.5 top-2 text-slate-400 hover:text-slate-600 text-xs cursor-pointer" > ✕  |
| UI-1273 | 332 | event-prop | onClick | {() => setSearchTerm('')} |
| UI-1274 | 349 | control | button | <button key={p} onClick={() => setFilterPriority(p)} className={&#96;px-2.5 py-1 rounded-md text-[11px] font-semibold transition-all cursor-pointer ${ isSelected ? p === 'HIGH' ? 'bg-rose-500 text-white shadow-xs' : p === 'MEDIUM' ? 'bg-amb |
| UI-1275 | 351 | event-prop | onClick | {() => setFilterPriority(p)} |
| UI-1276 | 375 | control | select | <select value={sortBy} onChange={(e) => setSortBy(e.target.value as SortOption)} className="bg-transparent border-none text-xs font-semibold text-slate-700 dark:text-slate-200 focus:outline-none cursor-pointer pr-1" > <option value="priorit |
| UI-1277 | 377 | event-prop | onChange | {(e) => setSortBy(e.target.value as SortOption)} |
| UI-1278 | 396 | control | input | <input type="checkbox" checked={isAllSelected} onChange={toggleSelectAll} className="w-4 h-4 rounded text-emerald-600 focus:ring-emerald-500 cursor-pointer" title="Chọn tất cả tác vụ hiển thị" /> </th> <th className="py-3 px-3 whitespace-no |
| UI-1279 | 399 | event-prop | onChange | {toggleSelectAll} |
| UI-1280 | 428 | event-prop | onClick | {() => { setSelectedTask(task); }} |
| UI-1281 | 438 | event-prop | onClick | {(e) => e.stopPropagation()} |
| UI-1282 | 439 | control | input | <input type="checkbox" checked={isChecked} onChange={(e) => toggleSelectTask(task.id, e as any)} className="w-4 h-4 rounded text-emerald-600 focus:ring-emerald-500 cursor-pointer" /> </td> {/* Record Code */} <td className="py-3 px-3 font-m |
| UI-1283 | 442 | event-prop | onChange | {(e) => toggleSelectTask(task.id, e as any)} |
| UI-1284 | 500 | event-prop | onClick | {(e) => e.stopPropagation()} |
| UI-1285 | 502 | control | button | <button onClick={() => { setSelectedTask(task); setViewLayout('split'); }} className="px-2.5 py-1 bg-slate-100 dark:bg-slate-800 hover:bg-emerald-50 hover:text-emerald-700 rounded text-[11px] font-semibold text-slate-700 dark:text-slate-300 |
| UI-1286 | 503 | event-prop | onClick | {() => { setSelectedTask(task); setViewLayout('split'); }} |
| UI-1287 | 513 | control | button | <button onClick={() => handleUnclaimTask(task)} className="p-1 text-slate-400 hover:text-amber-600 rounded cursor-pointer" title="Trả lại queue (Unclaim)" > <UserMinus className="w-3.5 h-3.5" />  |
| UI-1288 | 514 | event-prop | onClick | {() => handleUnclaimTask(task)} |
| UI-1289 | 521 | control | button | <button onClick={() => handleClaimTask(task)} className="p-1 text-slate-400 hover:text-emerald-600 rounded cursor-pointer" title="Nhận xử lý (Claim)" > <UserCheck className="w-3.5 h-3.5" />  |
| UI-1290 | 522 | event-prop | onClick | {() => handleClaimTask(task)} |
| UI-1291 | 551 | control | input | <input type="text" placeholder="Tìm kiếm tác vụ, mã hồ sơ..." value={searchTerm} onChange={(e) => setSearchTerm(e.target.value)} className="w-full pl-7 pr-3 py-1.5 text-xs bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-sl |
| UI-1292 | 555 | event-prop | onChange | {(e) => setSearchTerm(e.target.value)} |
| UI-1293 | 566 | control | select | <select value={filterPriority} onChange={(e) => setFilterPriority(e.target.value)} className="w-full px-2 py-1 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded text-xs font-semibold text-slate-700 dark:tex |
| UI-1294 | 568 | event-prop | onChange | {(e) => setFilterPriority(e.target.value)} |
| UI-1295 | 582 | control | select | <select value={sortBy} onChange={(e) => setSortBy(e.target.value as SortOption)} className="w-full px-2 py-1 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded text-xs font-semibold text-slate-700 dark:text- |
| UI-1296 | 584 | event-prop | onChange | {(e) => setSortBy(e.target.value as SortOption)} |
| UI-1297 | 599 | control | input | <input type="checkbox" checked={isAllSelected} onChange={toggleSelectAll} className="w-3.5 h-3.5 rounded text-emerald-600 focus:ring-emerald-500 cursor-pointer" /> <span>Chọn tất cả ({sortedAndFilteredTasks.length})</span> </label> <span cl |
| UI-1298 | 602 | event-prop | onChange | {toggleSelectAll} |
| UI-1299 | 621 | event-prop | onClick | {() => setSelectedTask(task)} |
| UI-1300 | 629 | event-prop | onClick | {(e) => e.stopPropagation()} |
| UI-1301 | 630 | control | input | <input type="checkbox" checked={isChecked} onChange={(e) => toggleSelectTask(task.id, e as any)} className="w-4 h-4 rounded text-emerald-600 focus:ring-emerald-500 cursor-pointer" /> </div> <div className="flex-1 min-w-0"> <div className="f |
| UI-1302 | 633 | event-prop | onChange | {(e) => toggleSelectTask(task.id, e as any)} |
| UI-1303 | 697 | control | button | <button onClick={() => handleUnclaimTask(selectedTask)} className="flex items-center gap-1.5 px-3 py-1.5 border border-slate-300 dark:border-slate-700 hover:bg-slate-100 text-slate-700 dark:text-slate-300 rounded text-xs font-semibold curso |
| UI-1304 | 698 | event-prop | onClick | {() => handleUnclaimTask(selectedTask)} |
| UI-1305 | 705 | control | button | <button onClick={() => handleClaimTask(selectedTask)} className="flex items-center gap-1.5 px-3 py-1.5 bg-emerald-700 hover:bg-emerald-800 text-white rounded text-xs font-semibold shadow-sm cursor-pointer" > <UserCheck className="w-3.5 h-3. |
| UI-1306 | 706 | event-prop | onClick | {() => handleClaimTask(selectedTask)} |
| UI-1307 | 758 | control | textarea | <textarea rows={3} defaultValue={field.value} onChange={(e) => setFormValues({ ...formValues, [field.id]: e.target.value })} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded tex |
| UI-1308 | 761 | event-prop | onChange | {(e) => setFormValues({ ...formValues, [field.id]: e.target.value })} |
| UI-1309 | 765 | control | select | <select defaultValue={field.value} onChange={(e) => setFormValues({ ...formValues, [field.id]: e.target.value })} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded text-xs font-s |
| UI-1310 | 767 | event-prop | onChange | {(e) => setFormValues({ ...formValues, [field.id]: e.target.value })} |
| UI-1311 | 776 | control | input | <input type="checkbox" defaultChecked={field.value} onChange={(e) => setFormValues({ ...formValues, [field.id]: e.target.checked })} className="rounded text-emerald-600 focus:ring-emerald-500" /> <span className="text-xs text-slate-700 dark |
| UI-1312 | 779 | event-prop | onChange | {(e) => setFormValues({ ...formValues, [field.id]: e.target.checked })} |
| UI-1313 | 785 | control | input | <input type={field.type} defaultValue={field.value} onChange={(e) => setFormValues({ ...formValues, [field.id]: e.target.value })} className="w-full px-3 py-2 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 round |
| UI-1314 | 788 | event-prop | onChange | {(e) => setFormValues({ ...formValues, [field.id]: e.target.value })} |
| UI-1315 | 804 | control | button | <button onClick={() => handleSubmitTask('SENDBACK')} className="flex items-center gap-1.5 px-3.5 py-2 border border-amber-300/80 dark:border-amber-700/80 text-amber-700 dark:text-amber-400 bg-amber-50/50 dark:bg-amber-950/20 hover:bg-amber- |
| UI-1316 | 805 | event-prop | onClick | {() => handleSubmitTask('SENDBACK')} |
| UI-1317 | 811 | control | button | <button onClick={() => handleSubmitTask('REJECT')} className="flex items-center gap-1.5 px-3.5 py-2 border border-rose-300/80 dark:border-rose-800/80 text-rose-700 dark:text-rose-400 bg-rose-50/50 dark:bg-rose-950/20 hover:bg-rose-100/60 da |
| UI-1318 | 812 | event-prop | onClick | {() => handleSubmitTask('REJECT')} |
| UI-1319 | 818 | control | button | <button onClick={() => handleSubmitTask('APPROVE')} className="flex items-center gap-1.5 px-4 py-2 bg-emerald-600 hover:bg-emerald-500 text-white rounded-lg text-xs font-semibold shadow-xs hover:shadow transition-all active:scale-[0.98] cur |
| UI-1320 | 819 | event-prop | onClick | {() => handleSubmitTask('APPROVE')} |
| UI-1321 | 857 | control | button | <button onClick={() => handleBulkAction('APPROVE')} className="flex items-center gap-1.5 px-3.5 py-1.5 bg-emerald-600 hover:bg-emerald-500 active:scale-[0.98] text-white rounded-lg text-xs font-semibold transition-all shadow-xs cursor-point |
| UI-1322 | 858 | event-prop | onClick | {() => handleBulkAction('APPROVE')} |
| UI-1323 | 865 | control | button | <button onClick={() => handleBulkAction('REJECT')} className="flex items-center gap-1.5 px-3.5 py-1.5 bg-rose-600 hover:bg-rose-500 active:scale-[0.98] text-white rounded-lg text-xs font-semibold transition-all shadow-xs cursor-pointer" > < |
| UI-1324 | 866 | event-prop | onClick | {() => handleBulkAction('REJECT')} |
| UI-1325 | 873 | control | button | <button onClick={() => setSelectedTaskIds([])} className="px-2.5 py-1.5 text-slate-400 hover:text-white hover:bg-slate-800 rounded-lg text-xs font-medium cursor-pointer transition-colors" > Bỏ chọn  |
| UI-1326 | 874 | event-prop | onClick | {() => setSelectedTaskIds([])} |

## main.tsx

| ID | Line | Kind | Name | Source excerpt |
|---|---:|---|---|---|
