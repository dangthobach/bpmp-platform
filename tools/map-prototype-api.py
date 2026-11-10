#!/usr/bin/env python3
"""Trace lexical prototype controls to design operations; never claim runtime parity."""
import csv
import json
import re
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'docs/refactor-prototype'


def main():
    inventory=json.loads((OUT/'interaction-inventory.json').read_text())
    catalog=json.loads((OUT/'api-catalog.json').read_text())
    operations={op['operationId'] for op in catalog['operations']}
    mappings={}
    def bind(names, *ids):
        for name in names.split():
            mappings[name]=list(ids)
    bind('handleDeployToEngine','targetCreateDeployment')
    bind('handleUpdateProcess','targetSaveProcessModelDraft')
    bind('handleRegisterCustomWorkflow handleCreateCustomWorkflow','targetInstantiateTemplate','targetCreateProcessModel')
    bind('handleTaskCompleted handleSubmitTask handleOfficerDecisionSubmit','completeWorkItem','targetSubmitWorkItemForm')
    bind('handleClaimTask','claimWorkItem');bind('handleUnclaimTask','unclaimWorkItem')
    bind('handleBulkAction','targetBatchWorkItemActions')
    bind('handleSaveEndpoint','targetCreateEndpoint','targetUpdateEndpoint')
    bind('handleCancelSelected','targetBatchInstanceActions','targetCancelInstance')
    bind('handleMigrateInstances','targetDryRunMigration','targetExecuteMigration')
    bind('handleMoveTokens','targetDryRunTokenMove','targetExecuteTokenMove')
    bind('handleManualRetry','targetRetryIncident')
    bind('handleSaveSnapshot handleSubmitSnapshot','targetCheckinDecisionModel')
    bind('handleRevertVersion','targetRevertDecisionModel')
    bind('handleRunEvaluation','targetEvaluateDecision')
    bind('handleExportDmn','targetExportDecisionModel')
    bind('handleSubmitNewRequest','targetCreateCustomerCase','targetSubmitCustomerCase')
    bind('handleClientAddDocs','targetCreateDocumentUpload','targetFinalizeDocumentUpload','targetAttachCaseDocument')
    bind('handleSendComment','targetAddCaseComment')
    bind('handlePerformLookup','targetQueryCustomerCaseLookup')
    bind('handleSaveDrawer','targetUpdatePolicy','targetPublishPolicy')
    bind('handleToggleProcessAction','targetUpdatePolicy','targetPublishPolicy')
    bind('handleCreateQueue','targetCreateQueue')
    bind('handleCreateUser','targetCreateUser')
    bind('handleBulkApplyDept handleBulkApplyPermissions handleBulkApplyRoles handleBulkApplySecurity handleBulkAssignPermsToUsers handleBulkDelete handleBulkExportCsv handleBulkLock handleBulkUpdateRoleAssignmentsToUsers','targetBatchUserActions')
    bind('handleBulkAssignPermsToRole handleBulkChangeRolesType handleBulkDeleteRoles handleBulkExportRoles handleBulkGrantPermsToRoles handleBulkRevokePermsFromRole','targetBatchRoleActions')
    bind('handleBulkExportPerms handleBulkToggleAuditLogging','targetBatchPermissionActions')
    bind('handleBulkExportResources handleBulkPingResources handleBulkSetResourceStatus','targetBatchResourceActions')
    bind('handleBulkExportServiceAccounts handleBulkRenewCertificates handleBulkRevokeServiceAccounts handleBulkRotateCertificates','targetBatchServiceAccountActions')
    bind('handleExportAuditCSV','targetCreateAccessAuditExport')
    local={
        'handleStartEdit','handleSelectDomainPreset','handleClearSelection','handleInvertSelection',
        'handleSelectActiveOnly','handleSelectLockedOnly','handleTogglePerm','handleToggleResource',
        'handleToggleRole','handleToggleSa','handleToggleSelectAll','handleToggleSelectAllPerms',
        'handleToggleSelectAllResources','handleToggleSelectAllRoles','handleToggleSelectAllSa','handleToggleUser',
        'handleToggleSelectRow','handleOpenDrawer','handleOpenCreateSnapshot','handleOpenCreateRuleModal',
        'handleOpenEditRuleModal','handleGenerateBusinessKey','handleSelectProcess','handleAddCustomVariable',
        'handleRemoveCustomVariable','handleResetCustomVariables','handlePersonaChange','handleOpenXml',
        'handleCopyXml','handleOpenXmlModal',
    }
    draft={
        'handleAddInputClause','handleAddOutputClause','handleAddRule','handleCellChange','handleDeleteRule',
        'handleDuplicateRule','handleHitPolicyChange','handleInjectSampleConflict','handleRemoveConflictRule',
        'handleSaveModalRule','handleAddPaletteNode','handleAppendNode','handleDeleteNode',
        'handleAddCandidateGroup','handleAddInputMapping','handleAddOutputMapping','handlePropChange',
        'handleRemoveCandidateGroup','handleRemoveInputMapping','handleRemoveOutputMapping',
        'handleToggleGranularAction','handleToggleRoleInDrawer',
    }
    for ids in mappings.values():
        assert set(ids)<=operations,ids
    rows=[]
    for entry in inventory['entries']:
        expression=entry['expression'];file=entry['file'];names=re.findall(r'\bhandle[A-Z]\w+',expression)
        ids=[];kind='REVIEW_REQUIRED';reason='Lexical source does not establish a backend intent; inspect handler/render state and walkthrough.'
        for name in names:
            ids.extend(mappings.get(name,[]))
        if 'handleDelete' in names and file.endswith('EndpointsModal.tsx'):
            ids.append('targetSoftDeleteEndpoint')
        if any(n in names for n in ('handleExport','handleExportCSV')):
            ids.append('targetCreateAccessAuditExport' if 'AccessAudit' in file else 'targetCreateReportExport')
        if 'handleRefresh' in names:
            ids.append('targetQueryPortalMetrics' if 'PortalMetrics' in file else 'targetQueryThroughput')
        if ids:
            kind='API_CANDIDATE';reason='Named callback/action mapped to design operation; verify request fields, permissions and runtime path.'
        elif any(n in draft for n in names):
            kind='LOCAL_DRAFT';reason='Editor/form draft operation; persist through owning save/check-in/publish action, not per keystroke.'
        elif any(n in local or n.startswith('handleMouse') or n.startswith('handleCanvasMouse') for n in names):
            kind='LOCAL_UI';reason='Selection/modal/navigation/editor view or dev persona input; business persistence requires separate action.'
        elif entry['name'].startswith(('onMouse','onPointer','onWheel','onDrag')):
            kind='LOCAL_UI';reason='Canvas/interaction event; verify keyboard/pointer parity, no standalone business endpoint inferred.'
        elif entry['name'] in ('onChange','onInput'):
            kind='LOCAL_DRAFT';reason='Input updates local form/filter. Review submit/query boundary; no per-field mutation endpoint inferred.'
        elif re.search(r'\bset(Show|Selected|ActiveTab|Search|Filter|Sort|Zoom|Pan|Wizard|Hovered|Collapsed|IsDark|View|Expanded|CurrentPage|Page)',expression):
            kind='LOCAL_UI';reason='Local view/filter/selection setter candidate; verify handler before treating as backend-free.'
        elif any(word in expression for word in ('onSelectModule','onSelectWorkspace','onOpenAdminConsole','onNavigateTab','onToggleCollapse','onToggleDarkMode','navigator.clipboard','stopPropagation()')):
            kind='LOCAL_UI';reason='Navigation/clipboard/event propagation candidate; policy of underlying data reads still applies.'
        # Explicit inline prototype actions which otherwise only mutate local arrays/toasts.
        if kind == 'REVIEW_REQUIRED':
            if entry['name'] == 'onSelectTenant':
                ids=['targetSwitchSessionTenant']; kind='API_CANDIDATE'
            elif 'setAllocationRules(' in expression:
                ids=['targetUpdateAssignmentPolicy']; kind='API_CANDIDATE'
            elif 'setActiveEditingItem(' in expression or 'setNewQueueRoles(' in expression:
                kind='LOCAL_DRAFT'
            elif 'setUploadedFiles(' in expression or 'onUploadAdditionalDoc' in expression:
                ids=['targetCreateDocumentUpload','targetFinalizeDocumentUpload','targetAttachCaseDocument']; kind='API_CANDIDATE'
            elif 'showToast(' in expression and ('tải tệp' in expression or 'tải xuống tệp' in expression):
                ids=['targetAuthorizeDocumentDownload']; kind='API_CANDIDATE'
            elif entry['name'] == 'onProcessInstanceCreated':
                ids=['targetCreateCustomerCase','targetSubmitCustomerCase']; kind='API_CANDIDATE'
            elif entry['name'] in ('onCompareVersions','onInspectVersion','onToggleExpand'):
                kind='LOCAL_UI'
            elif 'onCreateSnapshot' in entry['name'] or 'onSaveSnapshot' in entry['name']:
                ids=['targetCheckinDecisionModel']; kind='API_CANDIDATE'
            elif 'onRevert' in entry['name']:
                ids=['targetRevertDecisionModel']; kind='API_CANDIDATE'
            elif entry['name'] in ('onNotify','onClose') or 'copyToClipboard(' in expression:
                kind='LOCAL_UI'
            elif re.search(r'\bset(SubView|StatusFilter|ActiveWorkspace|SidebarCollapsed|NotificationsOpen|DrawerOpen|RequestDetailTab|PayloadViewMode|OfficerDecision|IsFullscreen|FitMode|VarViewMode|VarCategory|TimeRange|ActiveScopeTab|ActiveMetricTab|VisibleProcesses|CustomSteps)\b',expression):
                kind='LOCAL_UI'
            elif any(name in expression for name in ('onNavigateToModeler','onOpenEndpointsModal','onOpenHeatmap')):
                kind='LOCAL_UI'
            if kind != 'REVIEW_REQUIRED':
                reason='Explicit source-pattern candidate; verify persistence boundary and runtime behavior. Mock toast/array mutation is not a real backend API.'
        if kind == 'REVIEW_REQUIRED':
            if 'toggleUserLock(' in expression:
                ids=['targetLockUser','targetUnlockUser']; kind='API_CANDIDATE'
            elif 'setPermissionsList(' in expression:
                ids=['targetUpdatePermission']; kind='API_CANDIDATE'
            elif entry['name'] == 'onUpdateInstances' or 'onUpdateInstances(' in expression:
                ids=['targetBatchInstanceActions']; kind='API_CANDIDATE'
            elif 'onDeployDecision' in entry['name']:
                ids=['targetCreateDeployment']; kind='API_CANDIDATE'
            elif any(n in expression for n in ('onRevertToVersion','onRevert(')):
                ids=['targetRevertDecisionModel']; kind='API_CANDIDATE'
            elif 'onRunTest' in expression:
                ids=['targetEvaluateDecision']; kind='API_CANDIDATE'
            elif 'onExportDmn' in expression:
                ids=['targetExportDecisionModel']; kind='API_CANDIDATE'
            elif any(n in expression for n in ('onCompareVersions','onInspectVersion')):
                kind='LOCAL_UI'
            elif any(n in expression for n in ('onUpdateProcess','onUpdateNode','updateCurrentDecision','onAddNode')):
                kind='LOCAL_DRAFT'
            elif any(n in expression for n in ('onClose','onToggleExpand','toggleCategory(','onOpenDmn','new Blob(')):
                kind='LOCAL_UI'
            elif re.search(r'\bset(Bulk\w*|IsSelectionDropdownOpen|RoleViewMode|RoleTypeFilter|PermModuleFilter|IsSimulationActive|IsVersionHistoryOpen|HighlightedConflictId|TestInputs|IsCreatingSnapshot|ConfirmRevertSnapshot|EditingRuleOutputs|ActiveModelerMode|ActiveTool|TokenSimActive|SimPlaying|SimStepIndex|TargetEngine|ActiveInstanceDetail|ActiveChartType|SeverityFilter)\b',expression):
                kind='LOCAL_UI'
            elif entry['kind']=='event-prop' and '// applied' in expression:
                ids=['queryProcesses']; kind='API_CANDIDATE'
            elif entry['kind']=='event-prop' and any(n in expression for n in ('// apply filters','// Reload Heatmap')):
                ids=['targetQueryHeatmap']; kind='API_CANDIDATE'
            elif entry['kind']=='event-prop' and '// reset filters' in expression:
                kind='LOCAL_UI'
            if kind != 'REVIEW_REQUIRED':
                reason='Explicit source-pattern candidate; inspect owning handler/DTO and runtime before acceptance.'
        if kind == 'REVIEW_REQUIRED' and entry['kind']=='control' and not re.search(r'\bon[A-Z]\w*\s*=',expression):
            kind='NO_DIRECT_HANDLER'
            reason='No direct JSX callback in captured native control. Inspect enclosing form, wrapper/delegation, read-only/disabled state or placeholder before assigning an API.'
        if kind=='LOCAL_DRAFT':
            if '/modeler/' in file:
                ids=['targetSaveDecisionModelDraft' if 'Dmn' in file else 'targetSaveProcessModelDraft']
            elif 'TaskManagement' in file:
                ids=['targetUpdatePolicy','targetPublishPolicy']
        ids=sorted(set(ids));assert set(ids)<=operations,(entry['id'],ids)
        rows.append({'uiId':entry['id'],'file':file,'line':entry['line'],'kind':entry['kind'],'eventOrControl':entry['name'],
                     'classification':kind,'operationIds':';'.join(ids),'reviewState':'PENDING_SOURCE_AND_BROWSER_REVIEW',
                     'reason':reason,'sourceExcerpt':re.sub(r'\s+',' ',expression)[:300]})
    # Pair a native opening-tag callback with its separately inventoried event row.
    # This is lexical correlation only, not event propagation/runtime reachability proof.
    by_id={entry['id']:entry for entry in inventory['entries']}
    for row in rows:
        if row['kind']!='control' or row['classification']!='REVIEW_REQUIRED':
            continue
        entry=by_id[row['uiId']]
        match=re.search(r'\b(on[A-Z]\w*)\s*=\s*\{',entry['expression'])
        if not match:
            continue
        candidates=[other for other in rows if other['kind']=='event-prop' and other['file']==row['file']
                    and other['line']>=row['line'] and other['eventOrControl']==match[1]
                    and by_id[other['uiId']]['expression'] in entry['expression']]
        if candidates:
            nearest=min(candidates,key=lambda other:other['line'])
            if nearest['classification']!='REVIEW_REQUIRED':
                row['classification']=nearest['classification']
                row['operationIds']=nearest['operationIds']
                row['reason']='Lexically paired with '+nearest['uiId']+' callback; verify runtime propagation and actual action.'
    with (OUT/'ui-api-mapping.csv').open('w',newline='') as f:
        writer=csv.DictWriter(f,fieldnames=list(rows[0]));writer.writeheader();writer.writerows(rows)
    counts=Counter(r['classification'] for r in rows)
    lines=['# UI control/event → API traceability', '',
           'Generated lexical candidates, not verified semantic coverage. Every inventory row is included once; a control and its callback can represent the same interaction. All rows remain pending source/browser review. Do not count API_CANDIDATE as implemented or accepted parity.', '',
           'Full source hash/expression: [inventory JSON](interaction-inventory.json). Machine mapping: [CSV](ui-api-mapping.csv). Operation IDs: [API reference](api-operation-reference.md).', '',
           '| Classification | Rows |','|---|---:|']
    lines.extend(f'| {kind} | {count} |' for kind,count in sorted(counts.items()))
    lines += ['', '## Mapping', '', '| UI ID | File:line | Event/control | Classification | Operation IDs |', '|---|---|---|---|---|']
    for row in rows:
        lines.append(f"| {row['uiId']} | {row['file']}:{row['line']} | {row['eventOrControl']} | {row['classification']} | {row['operationIds']} |")
    lines += ['', '## Review rule', '',
              'REVIEW_REQUIRED blocks parity completion. Native controls without a direct handler can be form submitters, wrapped/delegated controls, disabled placeholders or unimplemented UI. Inspect the nearest form/component and handler body. Inline mutations and dynamic labels require manual mapping; do not add a guessed endpoint to inflate coverage.',
              'LOCAL_UI/LOCAL_DRAFT are lexical candidates too: changing a permission toggle might require immediate owner mutation while another toggle is staged until Save. Verify persistence boundary. Demo persona/sample conflicts stay development-only. Permanent delete requires Governance/retention design, not ordinary entity DELETE.']
    (OUT/'ui-api-mapping.md').write_text('\n'.join(lines)+'\n')
    print(f'{len(rows)} inventory rows: {dict(counts)}; all pending review')


if __name__=='__main__':
    main()
