#!/usr/bin/env python3
"""Build design-only API catalog from the deployed Gateway contract + explicit proposals.
Run from repository root. Does not replace the Gateway's embedded OpenAPI.
"""
import copy
import hashlib
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'docs/refactor-prototype'
SOURCE = ROOT / 'apps/go/api-gateway/internal/apidocs/openapi.json'
HTTP = {'get', 'post', 'put', 'patch', 'delete'}


def build():
    api = copy.deepcopy(json.loads(SOURCE.read_text()))
    api['info'] = {'title': 'BPMP API Design: Current Gateway + Proposed Full-feature Surface',
                   'version': '0.1.0-design', 'description': 'Design only. x-status distinguishes current-registration and proposed. Not a deployed API contract. Existing operations preserved verbatim except design metadata.'}
    api['servers'] = [{'url': 'https://bpmp.invalid', 'description': 'Documentation placeholder; not a deployed environment'}]
    api['x-baseline-sha256'] = hashlib.sha256(SOURCE.read_bytes()).hexdigest()
    schemas = api['components']['schemas']
    rows = []
    for path, item in api['paths'].items():
        for method, op in item.items():
            if method not in HTTP:
                continue
            owner = ('Authz' if '/access/catalog/' in path else
                     'Configuration' if '/configuration/' in path else
                     'Authoring' if '/authoring/' in path else
                     'Projection' if any(s in path for s in ('process-', 'saved-queries', 'saved-dashboards', 'query-exports', 'ops/query-plan')) else
                     'Engine' if '/workflows/' in path else
                     'Gateway' if '/runtime/' in path else 'Human')
            op['x-status'] = 'current-registration'
            op['x-owner'] = owner
            op['x-evidence'] = str(SOURCE.relative_to(ROOT))
            op['x-priority'] = 'P0'
            capability = op.get('x-capability', 'Existing typed policy; inspect owner contract')
            rows.append({'method': method.upper(), 'path': path, 'operationId': op['operationId'], 'owner': owner,
                         'priority': 'P0', 'status': 'current-registration', 'request': op.get('requestBody'), 'response': op['responses'], 'capability': capability})

    def ref(name):
        return {'$ref': '#/components/schemas/' + name}

    def typ(value):
        if value.startswith('['):
            return {'type': 'array', 'maxItems': 100, 'items': typ(value[1:-1])}
        if value.startswith('@'):
            return ref(value[1:])
        return copy.deepcopy({
            's': {'type': 'string', 'maxLength': 4096},
            'id': {'type': 'string', 'minLength': 1, 'maxLength': 256},
            'i': {'type': 'integer', 'minimum': 0, 'maximum': 9007199254740991},
            'b': {'type': 'boolean'},
            'time': {'type': 'string', 'format': 'date-time'},
            'json': {'type': 'object', 'additionalProperties': True, 'description': 'Versioned domain payload validated against owning schema; not arbitrary SQL/JavaScript.'},
            'uri': {'type': 'string', 'format': 'uri'},
            'decimal': {'type': 'string', 'pattern': r'^-?\d+(\.\d+)?$'},
        }[value])

    def schema(name, fields):
        props, required = {}, []
        for field in fields.split():
            key, value = field.split(':', 1)
            optional = key.endswith('?'); key = key.rstrip('?')
            props[key] = typ(value)
            if not optional:
                required.append(key)
        schemas[name] = {'type': 'object', 'additionalProperties': False, 'properties': props}
        if required:
            schemas[name]['required'] = required
        return name

    schema('TargetAuditEntity', 'id:id tenantId:id createdAt:time createdBy:id updatedAt:time updatedBy:id entityVersion:i isDeleted:b deletedAt?:time deletedBy?:id deleteReason?:s')
    schema('TargetReceipt', 'commandId:id resourceId:id status:s committedVersion?:i projectionVersion?:i statusUrl?:uri evidenceDigest?:s')
    schema('TargetJob', 'id:id status:s progress?:i resultUrl?:uri errorCode?:s entityVersion:i')
    schema('TargetValidation', 'valid:b revisionDigest:id issues:[@TargetIssue]')
    schema('TargetIssue', 'path:s code:id message:s')
    schema('TargetBatchItem', 'id:id expectedVersion:i action:id values?:json reason?:s')
    schema('TargetBatchResultItem', 'id:id status:s receipt?:@TargetReceipt error?:@TargetIssue')
    schema('TargetBatchResult', 'jobId?:id items:[@TargetBatchResultItem]')
    schema('TargetVersion', 'id:id number:i digest:id createdAt:time createdBy:id schemaVersion?:id content?:json')
    schema('TargetReference', 'key:id releaseId:id modelVersionId?:id')
    schema('TargetFreshness', 'producer:id checkpoint:s lastReconciledAt:time stale:b')
    schema('TargetQueryResult', 'rows:[json] nextCursor?:s sources:[@TargetFreshness] partial:b')
    schema('TargetArtifact', 'id:id uri:uri digest:id expiresAt?:time mediaType:s')
    schema('TargetDiagram', 'modelVersionId:id digest:id nodes:[json] edges:[json] executionOverlay?:json')
    schema('TargetCapability', 'resourceType:id actions:[id] supportedFeatures:[id] schemaVersion:id')
    schema('TargetResolvedMasterData', 'bundleId:id generation:i items:[json] sources:[@TargetFreshness]')

    def add(method, path, operation, owner, fields='', output='TargetReceipt', priority='P1', capability=None,
            collection=False, cas=None, note='', command=None):
        method = method.lower()
        existing = api['paths'].get(path, {}).get(method)
        if existing is not None:
            # A runtime-backed Gateway operation wins over an overlapping
            # proposal; it was already included in the current-operation rows.
            if existing.get('x-status') == 'current-registration':
                return
            raise AssertionError((method, path))
        mutation = method not in ('get',) and not operation.lower().startswith(('query', 'validate', 'preview', 'evaluate', 'resolve', 'compare', 'dryrun'))
        if command is not None:
            mutation = command
        if cas is None:
            cas = mutation and bool(re.search(r'\{\w+\}', path))
        params = [{'name': name, 'in': 'path', 'required': True, 'schema': typ('id')} for name in re.findall(r'\{(\w+)\}', path)]
        params += [{'name': 'X-BPMP-Tenant-ID', 'in': 'header', 'required': True, 'schema': typ('id'), 'description': 'Must match trusted authenticated tenant scope.'},
                   {'name': 'X-Correlation-ID', 'in': 'header', 'required': True, 'schema': typ('id')}]
        if method != 'get':
            params.append({'name': 'X-CSRF-Token', 'in': 'header', 'required': False, 'schema': typ('id'), 'description': 'Required with cookie auth on unsafe methods; bearer-only path follows existing contract.'})
        if mutation:
            params += [{'name': h, 'in': 'header', 'required': True, 'schema': typ('id')} for h in ('X-Command-ID', 'Idempotency-Key')]
        if cas:
            params.append({'name': 'If-Match', 'in': 'header', 'required': True, 'schema': typ('s'), 'description': 'Strong resource ETag. expectedVersion in body, when present, must agree.'})
        if collection:
            params += [{'name': 'cursor', 'in': 'query', 'schema': typ('s')},
                       {'name': 'pageSize', 'in': 'query', 'schema': {'type': 'integer', 'minimum': 1, 'maximum': 100, 'default': 50}},
                       {'name': 'search', 'in': 'query', 'schema': typ('s')},
                       {'name': 'includeDeleted', 'in': 'query', 'schema': typ('b'), 'description': 'Privileged capability; default excludes tombstones.'}]
            page = output + 'Page'
            if page not in schemas:
                schema(page, 'items:[@' + output + '] nextCursor?:s')
            output = page
        code = '200' if method == 'get' or not mutation else '202'
        responses = {code: {'description': 'Authorized result; 202 is acceptance, not projection convergence.',
                            'headers': {'ETag': {'schema': {'type': 'string'}, 'description': 'When response represents a versioned mutable entity.'}},
                            'content': {'application/json': {'schema': ref(output)}}}}
        for error, description in {'400':'Malformed input', '401':'Unauthenticated', '403':'Forbidden', '404':'Not found or not visible', '409':'State/version conflict or idempotency payload mismatch', '412':'ETag mismatch', '413':'Payload or upload too large', '410':'Cursor or artifact expired', '422':'Domain validation failed', '428':'Missing concurrency precondition', '429':'Rate/cost limit', '503':'Required owner/snapshot unavailable'}.items():
            responses[error] = {'description': description, 'content': {'application/problem+json': {'schema': ref('Problem')}}}
        operation_id = 'target' + operation[0].upper() + operation[1:]
        request = None
        if fields:
            request_name = schema(operation_id + 'Request', fields)
            request = {'required': True, 'content': {'application/json': {'schema': ref(request_name)}}}
        op = {'operationId': operation_id, 'tags': [owner], 'summary': operation,
              'description': 'PROPOSED; not implemented. ' + note + ' Entity/audit/precondition conventions apply. Scope/auth enforced by owner.',
              'parameters': params, 'responses': responses, 'x-status': 'proposed', 'x-owner': owner,
              'x-priority': priority, 'x-capability': capability or owner.lower() + '.' + operation,
              'x-entity-precondition': bool(cas)}
        if request:
            op['requestBody'] = request
        api['paths'].setdefault(path, {})[method] = op
        rows.append({'method': method.upper(), 'path': path, 'operationId': operation_id, 'owner': owner,
                     'priority': priority, 'status': 'proposed', 'capability': op['x-capability'], 'request': request, 'response': responses})

    # Core runtime: no generic external arbitrary CommandEnvelope/Raft API.
    add('GET','/v1/runtime/capabilities','getRuntimeCapabilities','Engine',output='TargetCapability',priority='P0')
    add('GET','/v1/commands/{commandID}','getCommandReceipt','Engine',output='TargetReceipt',priority='P0')
    add('GET','/v1/process-instances/{instanceID}','getInstance','Projection',output='WorkflowInstanceReadModel',priority='P0')
    add('GET','/v1/process-instances/{instanceID}/diagram','getInstanceDiagram','Projection',output='TargetDiagram',priority='P0')
    add('GET','/v1/process-instances/{instanceID}/variables','getInstanceVariables','Projection',output='TargetQueryResult',priority='P0')
    add('GET','/v1/process-instances/{instanceID}/history','getInstanceHistory','Projection',output='TargetQueryResult',priority='P0')
    for action in ('cancel','suspend','resume'):
        add('POST',f'/v1/process-instances/{{instanceID}}/{action}',action+'Instance','Engine','expectedVersion:i reason:s approvalId?:id',priority='P0',note='Requires new verified Engine command semantics; not present in HandleCommand oneof today.')
    for action in ('migration','token-move'):
        base=f'/v1/process-instances/{{instanceID}}/{action}-plans'
        add('POST',base,'dryRun'+action.title().replace('-',''),'Engine','expectedVersion:i targetModelVersionId?:id tokenMappings:json variableMappings:json reason:s',output='TargetValidation',priority='P0',cas=False,note='Bounded dry-run; execution gated on supported compiler/Engine semantics.')
        add('POST',base+'/{planID}/execute','execute'+action.title().replace('-',''),'Engine','expectedVersion:i planDigest:id approvalId?:id reason:s',priority='P0')
    add('PATCH','/v1/process-instances/{instanceID}/variables','updateInstanceVariables','Engine','expectedVersion:i changes:json reason:s',priority='P0',note='Typed schema and transition constraints; not a projection patch.')
    add('POST','/v1/process-instances/batch-actions','batchInstanceActions','Engine','items:[@TargetBatchItem]',output='TargetBatchResult',priority='P0',cas=False)
    add('GET','/v1/incidents','listIncidents','Projection',output='TargetJob',collection=True,priority='P0')
    add('POST','/v1/incidents/{incidentID}/retry','retryIncident','Engine','expectedVersion:i reason:s',priority='P0',note='Validate task/job lease and max attempts, not local status reset.')
    add('GET','/v1/work-items/{workItemID}/form','getWorkItemForm','Human',output='TargetVersion',priority='P0')
    add('POST','/v1/work-items/{workItemID}/validate','validateWorkItemForm','Human','formVersionId:id values:json outcome:s',output='TargetValidation',priority='P0',cas=False)
    add('POST','/v1/work-items/batch-actions','batchWorkItemActions','Human','items:[@TargetBatchItem]',output='TargetBatchResult',priority='P0',cas=False)
    add('GET','/v1/work-items/{workItemID}/history','getWorkItemHistory','Human',output='ListAuditRecordsResponse',priority='P0')
    add('POST','/v1/runtime-cases','activateRuntimeCase','Engine','caseModelVersionId:id caseId:id initialFacts:json',priority='P0',cas=False)
    add('POST','/v1/runtime-cases/{caseID}/sentries/evaluate','evaluateRuntimeSentries','Engine','expectedVersion:i facts:json',priority='P0',cas=True,command=True)
    add('GET','/v1/jobs/{jobID}','getJob','Gateway',output='TargetJob')
    add('POST','/v1/jobs/{jobID}/cancel','cancelJob','Gateway','expectedVersion:i reason:s')

    def resource(base, singular, owner, fields, priority='P1', delete=True):
        target='Target'+singular
        schema(target, fields)
        schemas[target]['properties'].update(copy.deepcopy(schemas['TargetAuditEntity']['properties']))
        schemas[target].setdefault('required',[]).extend(schemas['TargetAuditEntity']['required'])
        add('GET',base,'list'+singular,owner,output=target,priority=priority,collection=True)
        add('POST',base,'create'+singular,owner,fields,priority=priority,cas=False)
        item=base+'/{'+singular[0].lower()+singular[1:]+'ID}'
        add('GET',item,'get'+singular,owner,output=target,priority=priority)
        # PATCH fields optional; owner enforces not-empty and cross-field constraints.
        patch=' '.join(k.rstrip('?')+'?:'+v for k,v in (f.split(':',1) for f in fields.split()))
        add('PATCH',item,'update'+singular,owner,'expectedVersion:i '+patch,priority=priority)
        if delete:
            add('DELETE',item,'softDelete'+singular,owner,'expectedVersion:i reason:s',priority=priority)
            add('POST',item+'/restore','restore'+singular,owner,'expectedVersion:i reason:s',priority=priority)
        return item

    # Authoring owns artifacts; runtime Engine deployability still explicit.
    for noun,path,fields in (
        ('ProcessModel','process-models','key:id name:s category?:s description?:s engineProfile:id'),
        ('DecisionModel','decision-models','key:id name:s hitPolicy:s description?:s'),
        ('FormDefinition','forms','key:id name:s schema:json uiSchema:json'),
        ('WorkflowTemplate','workflow-templates','key:id name:s domain:id graph:json parameterSchema:json')):
        item=resource('/v1/authoring/'+path,noun,'Authoring',fields,'P0')
        add('GET',item+'/versions','list'+noun+'Versions','Authoring',output='TargetVersion',collection=True,priority='P0')
        add('GET',item+'/versions/{versionID}','get'+noun+'Version','Authoring',output='TargetVersion',priority='P0')
        add('GET',item+'/draft','get'+noun+'Draft','Authoring',output='TargetVersion',priority='P0')
        add('PUT',item+'/draft','save'+noun+'Draft','Authoring','expectedVersion:i baseVersionId?:id definition:json',priority='P0')
        add('POST',item+'/draft/validate','validate'+noun,'Authoring','revisionDigest:id',output='TargetValidation',priority='P0',cas=False)
        add('POST',item+'/versions','checkin'+noun,'Authoring','expectedVersion:i revisionDigest:id versionTag:id summary:s',priority='P0')
        add('POST',item+'/compare','compare'+noun,'Authoring','baseVersionId:id targetVersionId:id',output='TargetQueryResult',priority='P0',cas=False)
        add('POST',item+'/revert','revert'+noun,'Authoring','expectedVersion:i sourceVersionId:id reason:s',priority='P0',note='Creates new draft; never rewrites immutable published content.')
        add('GET',item+'/versions/{versionID}/artifact','export'+noun,'Authoring',output='TargetArtifact',priority='P0')
    add('POST','/v1/authoring/imports','importModelArtifact','Authoring','artifactId:id modelKind:id targetModelId?:id',output='TargetJob',priority='P0',cas=False)
    add('POST','/v1/authoring/deployments','createDeployment','Authoring','modelVersionId:id environment:id engineProfile:id approvalId?:id',output='TargetJob',priority='P0',cas=False)
    add('GET','/v1/authoring/deployments/{deploymentID}','getDeployment','Authoring',output='TargetJob',priority='P0')
    add('POST','/v1/authoring/decisions/{decisionVersionID}/evaluations','evaluateDecision','Authoring','inputs:json trace:b',output='TargetQueryResult',priority='P0',cas=False)
    add('POST','/v1/authoring/workflow-templates/{templateID}/instantiate','instantiateTemplate','Authoring','templateVersionId:id modelKey:id parameters:json',priority='P0',cas=False)

    # Customer Case is not Human runtime case. Avoid collision with current /v1/cases/{caseID}.
    case=resource('/v1/customer-cases','CustomerCase','Case','serviceOfferingVersionId:id businessKey:id customerRef:id branchRef?:id values:json',delete=False)
    for action in ('submit','withdraw'):
        add('POST',case+'/'+action,action+'CustomerCase','Case','expectedVersion:i reason?:s')
    add('POST','/v1/customer-cases/lookup','queryCustomerCaseLookup','Case','trackingCode?:id instanceId?:id taxId?:s',output='TargetQueryResult',cas=False)
    add('GET',case+'/timeline','getCustomerCaseTimeline','Case',output='TargetQueryResult')
    add('GET',case+'/documents','listCaseDocuments','Case',output='TargetReference',collection=True)
    add('POST',case+'/documents','attachCaseDocument','Case','expectedVersion:i documentVersionId:id requirementKey:id')
    add('DELETE',case+'/documents/{documentID}','detachCaseDocument','Case','expectedVersion:i reason:s')
    add('GET',case+'/comments','listCaseComments','Case',output='TargetQueryResult')
    add('POST',case+'/comments','addCaseComment','Case','expectedVersion:i body:s visibility:id')
    resource('/v1/service-offerings','ServiceOffering','Case','key:id name:s processVersionId:id formVersionId:id documentRequirements:json presentation:json')
    add('POST','/v1/service-offerings/{serviceOfferingID}/publish','publishServiceOffering','Case','expectedVersion:i revisionDigest:id')

    doc=resource('/v1/documents','Document','Document','key:id ownerKind:id ownerRef:id classification:id',delete=True)
    add('POST','/v1/documents/uploads','createDocumentUpload','Document','documentId:id fileName:s mediaType:s byteSize:i checksum:id',output='TargetJob',cas=False)
    add('POST','/v1/documents/uploads/{uploadID}/finalize','finalizeDocumentUpload','Document','expectedVersion:i checksum:id byteSize:i')
    add('GET',doc+'/versions','listDocumentVersions','Document',output='TargetVersion',collection=True)
    add('POST',doc+'/verify','verifyDocument','Document','expectedVersion:i documentVersionId:id verdict:id reason:s')
    add('POST',doc+'/download','authorizeDocumentDownload','Document','documentVersionId:id',output='TargetArtifact',cas=False)
    add('POST',doc+'/holds','createDocumentHold','Document','expectedVersion:i reason:s')

    for base,noun,owner,fields in (
        ('/v1/admin/users','User','Identity','username:id fullName:s email:s departmentRef?:id'),
        ('/v1/admin/roles','Role','Authz','code:id name:s roleType:id'),
        ('/v1/admin/permissions','Permission','Authz','code:id resourceType:id action:id mandatoryAudit:b'),
        ('/v1/admin/queues','Queue','Human','key:id name:s processKey:id'),
        ('/v1/admin/service-accounts','ServiceAccount','Identity','name:s workloadRef:id'),
        ('/v1/admin/endpoints','Endpoint','Integration','key:id name:s url:uri httpMethod:id authType:id secretRef?:id'),
        ('/v1/admin/notification-templates','NotificationTemplate','Notification','key:id name:s channel:id locale:id body:s variableSchema:json')):
        resource(base,noun,owner,fields)
        add('POST',base+'/batch-actions','batch'+noun+'Actions',owner,'items:[@TargetBatchItem]',output='TargetBatchResult',cas=False)
    for action in ('lock','unlock','revoke-sessions'):
        add('POST','/v1/admin/users/{userID}/'+action,action.title().replace('-','')+'User','Identity','expectedVersion:i reason:s')
    add('PUT','/v1/admin/users/{userID}/roles','setUserRoles','Authz','expectedVersion:i roleIds:[id] scope:json')
    add('PUT','/v1/admin/users/{userID}/permissions','setUserPermissions','Authz','expectedVersion:i grants:[json] reason:s')
    add('PUT','/v1/admin/roles/{roleID}/permissions','setRolePermissions','Authz','expectedVersion:i permissionIds:[id] reason:s')
    resource('/v1/admin/policies','Policy','Authz','key:id resourceType:id resourceRef:id rules:json')
    for action in ('validate','publish','revoke'):
        add('POST','/v1/admin/policies/{policyID}/'+action,action+'Policy','Authz','expectedVersion:i revisionDigest:id reason:s',output='TargetValidation' if action=='validate' else 'TargetReceipt',cas=action!='validate')
    for action in ('rotate','renew','revoke'):
        add('POST','/v1/admin/service-accounts/{serviceAccountID}/certificates/'+action,action+'WorkloadCertificate','Identity','expectedVersion:i reason:s',output='TargetJob')
    add('POST','/v1/admin/endpoints/{endpointID}/probe','probeEndpoint','Integration','expectedVersion:i',output='TargetJob')
    add('GET','/v1/admin/resources','listIntegrationResources','Integration',output='TargetCapability',collection=True)
    add('POST','/v1/admin/resources/batch-actions','batchResourceActions','Integration','items:[@TargetBatchItem]',output='TargetBatchResult',cas=False)
    add('POST','/v1/admin/access-audit/query','queryAccessAudit','Audit','filter:json cursor?:s pageSize?:i',output='TargetQueryResult',cas=False)
    add('POST','/v1/admin/access-audit/exports','createAccessAuditExport','Audit','filter:json fields:[id]',output='TargetJob',cas=False)
    add('POST','/v1/notifications/preferences','updateNotificationPreferences','Notification','expectedVersion:i channels:json',cas=True)

    # MDM models/datasets are administrative entities; releases are immutable.
    for noun,path,fields in (
        ('MasterDataModel','models','key:id name:s fieldSchema:json constraints:json uiSchema:json'),
        ('MasterDataset','datasets','key:id name:s modelVersionId:id sourceOwner:id')):
        item=resource('/v1/mdm/'+path,noun,'MDM',fields)
        add('GET',item+'/versions','list'+noun+'Versions','MDM',output='TargetVersion',collection=True)
        add('POST',item+'/validate','validate'+noun,'MDM','expectedVersion:i revisionDigest:id',output='TargetValidation',cas=False)
    add('POST','/v1/mdm/datasets/{datasetID}/records/batch','upsertMasterRecords','MDM','expectedVersion:i records:[json]')
    add('POST','/v1/mdm/datasets/{datasetID}/imports','importMasterRecords','MDM','expectedVersion:i artifactId:id',output='TargetJob')
    add('GET','/v1/mdm/datasets/{datasetID}/records','listMasterRecords','MDM',output='TargetVersion',collection=True)
    add('DELETE','/v1/mdm/datasets/{datasetID}/records/{recordID}','tombstoneMasterRecord','MDM','expectedVersion:i reason:s')
    add('POST','/v1/mdm/publications/preview','previewMasterPublication','MDM','publicationScope:id datasetReleases:[id] revisionDigest:id baseGeneration:i',output='TargetValidation',cas=False)
    add('POST','/v1/mdm/publications','publishMasterBundle','MDM','publicationScope:id datasetReleases:[id] revisionDigest:id baseGeneration:i approvalId?:id effectiveAt?:time',cas=True)
    add('POST','/v1/mdm/publications/{publicationID}/rollback','rollbackMasterPublication','MDM','expectedVersion:i sourceBundleId:id reason:s')
    add('POST','/v1/mdm/resolve-batch','resolveMasterData','MDM','bundleId?:id consistencyMode:id keys:[@TargetReference] queries?:[json]',output='TargetResolvedMasterData',cas=False)
    add('GET','/v1/mdm/snapshots/{bundleID}','getMasterSnapshot','MDM',output='TargetArtifact')
    add('GET','/v1/mdm/snapshots/{bundleID}/chunks/{chunkID}','getMasterSnapshotChunk','MDM',output='TargetArtifact')
    add('POST','/v1/mdm/changes/query','queryMasterChanges','MDM','resumeToken:s scope:id',output='TargetQueryResult',cas=False)
    resource('/v1/mdm/subscriptions','MasterSubscription','MDM','workloadRef:id datasetIds:[id] viewScope:json supportedSchemas:[id]')
    add('GET','/v1/mdm/consumers','listMasterConsumers','MDM',output='TargetQueryResult')
    add('POST','/v1/mdm/consumers/{consumerID}/resync','resyncMasterConsumer','MDM','expectedVersion:i reason:s',output='TargetJob')

    # Reporting uses local facts/dimensions; never SQL on business service DB.
    for feature in ('heatmap','throughput','health','portal-metrics'):
        add('POST','/v1/analytics/'+feature,'query'+feature.title().replace('-',''),'Report','filter:json metricVersion:id sourceCut?:json',output='TargetQueryResult',cas=False)
    resource('/v1/reports','ReportDefinition','Report','key:id name:s query:json metrics:[id]')
    add('POST','/v1/reports/query','queryReport','Report','reportVersionId:id filters:json sourceCut?:json',output='TargetQueryResult',cas=False)
    add('POST','/v1/report-exports','createReportExport','Report','reportVersionId:id filters:json format:id sourceCut?:json',output='TargetJob',cas=False)
    add('GET','/v1/report-exports/{exportID}','getReportExport','Report',output='TargetJob')
    add('POST','/v1/report-exports/{exportID}/cancel','cancelReportExport','Report','expectedVersion:i reason:s')
    add('POST','/v1/report-exports/{exportID}/download','authorizeReportDownload','Report','artifactId:id',output='TargetArtifact',cas=False)
    resource('/v1/report-schedules','ReportSchedule','Report','reportVersionId:id cron:s timeZone:id recipients:[id]')
    # Browser/session producer is external; no fake login endpoint.
    add('GET','/v1/session/context','getSessionContext','Session',output='TargetCapability')
    add('POST','/v1/session/tenant','switchSessionTenant','Session','tenantRef:id',cas=False,note='External session producer contract required; reauthorize membership and invalidate old scope.')
    add('GET','/v1/preferences','getUserPreferences','Session',output='TargetQueryResult')
    add('PUT','/v1/preferences','setUserPreferences','Session','expectedVersion:i preferences:json',cas=True)

    # Additional runtime feature surface; execution gated by capabilities/verified Engine commands.
    add('GET','/v1/workflows','listRunnableWorkflows','Engine',output='TargetVersion',collection=True,priority='P0')
    add('GET','/v1/workflows/{workflowType}/versions','listRunnableWorkflowVersions','Engine',output='TargetVersion',collection=True,priority='P0')
    for event in ('messages','signals'):
        add('POST','/v1/runtime/'+event,'correlate'+event.title(),'Engine','name:id correlationKey:id payload:json expectedVersion?:i',priority='P0',cas=False,note='Planned capability; scope/correlation uniqueness and supported BPMN semantics required.')
    add('POST','/v1/process-instances/{instanceID}/variables/validate','validateInstanceVariables','Engine','expectedVersion:i changes:json',output='TargetValidation',priority='P0',cas=False)
    add('GET','/v1/runtime/workers','listRuntimeWorkers','Engine',output='TargetQueryResult',priority='P0')
    add('GET','/v1/runtime/service-jobs','listServiceJobs','Engine',output='TargetJob',collection=True,priority='P0')
    add('POST','/v1/runtime/service-jobs/{jobID}/retry','retryServiceJob','Engine','expectedVersion:i reason:s',priority='P0')
    add('POST','/v1/runtime/service-jobs/{jobID}/cancel','cancelServiceJob','Engine','expectedVersion:i reason:s',priority='P0')
    add('POST','/v1/work-items/{workItemID}/submit-form','submitWorkItemForm','Human','expectedVersion:i formVersionId:id values:json outcome:id reason?:s masterDataBundleId?:id',priority='P0',note='Proposed richer form facade, shares existing completion authority; not a second transition path.')
    add('POST','/v1/mdm/models/{masterDataModelID}/versions','publishMasterSchema','MDM','expectedVersion:i revisionDigest:id compatibilityReportId:id')
    add('POST','/v1/mdm/datasets/{datasetID}/releases','createMasterRelease','MDM','expectedVersion:i revisionDigest:id validationReportId:id')
    add('GET','/v1/mdm/datasets/{datasetID}/releases','listMasterReleases','MDM',output='TargetVersion',collection=True)
    add('POST','/v1/mdm/datasets/{datasetID}/records/{recordID}/restore','restoreMasterRecord','MDM','expectedVersion:i reason:s')
    add('PUT','/v1/mdm/datasets/{datasetID}/relationships','updateMasterRelationships','MDM','expectedVersion:i relationships:[json]')
    add('PUT','/v1/mdm/datasets/{datasetID}/source-mappings','updateMasterSourceMappings','MDM','expectedVersion:i mappings:[json]')
    for action in ('create','record','submit'):
        add('POST','/v1/governance/approvals'+('' if action=='create' else '/{approvalID}/'+action),action+'GovernanceApproval','Governance','expectedVersion?:i targetRef:id action:id proof?:json reason:s',priority='P0',cas=action!='create')
    add('GET','/v1/governance/approvals/{approvalID}','getGovernanceApproval','Governance',output='TargetJob',priority='P0')

    # Per-endpoint action allowlists; envelopes still require owner-specific discriminated schemas before release.
    batch_actions = {
        'targetBatchUserActions': ['LOCK','UNLOCK','SET_DEPARTMENT','ADD_ROLES','REMOVE_ROLES','SET_PERMISSIONS','SET_SECURITY','SOFT_DELETE','EXPORT'],
        'targetBatchRoleActions': ['GRANT_PERMISSIONS','REVOKE_PERMISSIONS','CHANGE_TYPE','ASSIGN_USERS','REMOVE_USERS','SOFT_DELETE','EXPORT'],
        'targetBatchPermissionActions': ['ASSIGN_ROLE','REVOKE_ROLE','ASSIGN_USER','REVOKE_USER','SET_AUDIT_POLICY','EXPORT'],
        'targetBatchResourceActions': ['PROBE','SET_STATUS','EXPORT'],
        'targetBatchServiceAccountActions': ['ROTATE_CERTIFICATE','RENEW_CERTIFICATE','REVOKE','EXPORT_METADATA'],
        'targetBatchWorkItemActions': ['APPROVE','REJECT'],
        'targetBatchInstanceActions': ['CANCEL','EXECUTE_MIGRATION_PLAN','EXECUTE_TOKEN_MOVE_PLAN'],
    }
    for item in api['paths'].values():
        for method, op in item.items():
            if method not in HTTP:
                continue
            if op['operationId'] in batch_actions:
                op['x-allowed-actions'] = batch_actions[op['operationId']]
                request_name=op['requestBody']['content']['application/json']['schema']['$ref'].split('/')[-1]
                item_schema=copy.deepcopy(schemas['TargetBatchItem'])
                item_schema['properties']['action']['enum']=batch_actions[op['operationId']]
                schemas[request_name]['properties']['items']['items']=item_schema

    # Record exact internal RPC signatures, including worker/Raft-only surfaces.
    rpc_rows=[]
    for path in sorted((ROOT/'contracts/proto').rglob('*.proto')):
        text=path.read_text()
        package=re.search(r'package\s+([\w.]+);',text)[1]
        for service,body in re.findall(r'service\s+(\w+)\s*\{(.*?)\}',text,re.S):
            for name,request,response in re.findall(r'rpc\s+(\w+)\s*\((.*?)\)\s*returns\s*\((.*?)\)',body,re.S):
                rpc_rows.append({'service':package+'.'+service,'rpc':name,'request':request.strip(),'response':response.strip(),'source':str(path.relative_to(ROOT)),'exposure':'internal-mTLS; worker/Raft/admin restrictions apply'})
    for name, body in schemas.items():
        props = body.get('properties', {})
        if name.startswith('target') and 'consistencyMode' in props:
            props['consistencyMode']['enum'] = ['PINNED', 'BOUNDED_STALENESS', 'AUTHORITATIVE_AT_RESOLVE']
        if name == 'targetSubmitWorkItemFormRequest':
            props['outcome']['enum'] = ['APPROVE', 'REJECT', 'SENDBACK']
    # New entity UUIDs are not arbitrary IDs/keys. Keep deployed legacy contracts untouched.
    uuid_fields = {'id','tenantId','resourceId','modelVersionId','formVersionId','sourceVersionId',
                   'baseVersionId','targetVersionId','targetModelVersionId','templateVersionId','approvalId',
                   'documentId','documentVersionId','serviceOfferingVersionId','datasetId','releaseId',
                   'bundleId','sourceBundleId','reportVersionId','artifactId','validationReportId',
                   'compatibilityReportId','processVersionId','masterDataBundleId','policyId','queueId'}
    uuid_arrays = {'roleIds','permissionIds','datasetIds','datasetReleases'}
    for name, body in schemas.items():
        if not name.startswith(('Target','target')):
            continue
        for field, field_schema in body.get('properties',{}).items():
            if field in uuid_fields and field_schema.get('type')=='string':
                field_schema['format']='uuid'
            elif field in uuid_arrays and field_schema.get('type')=='array':
                field_schema['items']['format']='uuid'
    for path, item in api['paths'].items():
        for method, op in item.items():
            if method not in HTTP or op.get('x-status')!='proposed':
                continue
            for parameter in op.get('parameters',[]):
                if parameter['in']!='path':
                    continue
                name=parameter['name']
                legacy = name in {'workflowType','instanceID','workItemID','caseID','commandID','chunkID'} or path.startswith('/v1/runtime/service-jobs')
                if name.endswith('ID') and not legacy:
                    parameter['schema']['format']='uuid'
                elif legacy:
                    parameter['description']='Existing opaque reference or business/chunk key; explicit compatibility mapping required. New owned entity IDs are UUIDs.'

    OUT.mkdir(parents=True,exist_ok=True)
    (OUT/'api-spec.openapi.json').write_text(json.dumps(api,ensure_ascii=False,indent=2)+'\n')
    (OUT/'api-catalog.json').write_text(json.dumps({'baselineSha256':api['x-baseline-sha256'],'operations':rows,'internalRpcs':rpc_rows},ensure_ascii=False,indent=2)+'\n')
    lines=['# API operation reference', '', 'Generated by `tools/build-api-design.py`. Current-registration is Gateway contract/route evidence, not proof of production deployment. Proposed is design only. Full schemas are in [OpenAPI](api-spec.openapi.json).', '', '| Operation ID | Method / path | Owner | Priority | Status | Capability |', '|---|---|---|---|---|---|']
    for r in rows:
        lines.append(f"| {r['operationId']} | `{r['method']} {r['path']}` | {r['owner']} | {r['priority']} | {r['status']} | {r['capability']} |")
    lines += ['', '## Operation payload reference', '', 'Schema references resolve in the design OpenAPI. Existing request/response shapes are preserved; generic legacy objects remain documented contract gaps, not newly typed DTOs.']
    for r in rows:
        req=(r['request'] or {}).get('content',{}).get('application/json',{}).get('schema')
        lines += ['', '### '+r['operationId'], '', f"`{r['method']} {r['path']}` — {r['status']}; owner {r['owner']}."]
        if req and '$ref' in req:
            name=req['$ref'].split('/')[-1]; body=schemas[name]
            fields=body.get('properties',{})
            lines.append('Request: `'+name+'`. Required: '+', '.join(body.get('required',[]))+'.')
            if fields:
                lines.append('Fields: '+', '.join('`'+n+'`: '+(v.get('type') or v.get('$ref','object').split('/')[-1])+((' — '+v['description']) if v.get('description') else '') for n,v in fields.items())+'.')
        else:
            lines.append('Request: '+('no JSON body' if req is None else 'legacy generic object; consult owning Protobuf/handler before client generation')+'.')
        lines.append('Responses: '+', '.join(f'`{code}` '+response.get('description','') for code,response in r['response'].items())+'.')
    lines += ['', '## Internal gRPC inventory', '', '| Service/RPC | Request | Response | Source |', '|---|---|---|---|']
    for r in rpc_rows:
        lines.append(f"| {r['service']}/{r['rpc']} | {r['request']} | {r['response']} | {r['source']} |")
    (OUT/'api-operation-reference.md').write_text('\n'.join(lines)+'\n')
    print(f'{len(rows)} HTTP operations ({sum(r["status"]=="current-registration" for r in rows)} current); {len(rpc_rows)} internal RPCs')


if __name__=='__main__':
    build()
