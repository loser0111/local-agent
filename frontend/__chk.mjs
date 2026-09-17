export namespace main {
	
	export class AuditEntry {
	    time: number;
	    sessionId: string;
	    tool: string;
	    subject: string;
	    decision: string;
	    stage: string;
	    reason: string;
	    rule?: string;
	    source?: string;
	
	    static createFrom(source: any = {}) {
	        return new AuditEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.sessionId = source["sessionId"];
	        this.tool = source["tool"];
	        this.subject = source["subject"];
	        this.decision = source["decision"];
	        this.stage = source["stage"];
	        this.reason = source["reason"];
	        this.rule = source["rule"];
	        this.source = source["source"];
	    }
	}
	export class PlanStep {
	    index: number;
	    title: string;
	    detail?: string;
	    status: string;
	    summary?: string;
	    error?: string;
	    startedAt?: number;
	    finishedAt?: number;
	
	    static createFrom(source: any = {}) {
	        return new PlanStep(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.title = source["title"];
	        this.detail = source["detail"];
	        this.status = source["status"];
	        this.summary = source["summary"];
	        this.error = source["error"];
	        this.startedAt = source["startedAt"];
	        this.finishedAt = source["finishedAt"];
	    }
	}
	export class Plan {
	    id: string;
	    sessionId: string;
	    title: string;
	    status: string;
	    steps: PlanStep[];
	    createdAt: number;
	    updatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Plan(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.title = source["title"];
	        this.status = source["status"];
	        this.steps = this.convertValues(source["steps"], PlanStep);
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DiffLine {
	    type: string;
	    oldLineNo: number;
	    newLineNo: number;
	    content: string;
	
	    static createFrom(source: any = {}) {
	        return new DiffLine(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.oldLineNo = source["oldLineNo"];
	        this.newLineNo = source["newLineNo"];
	        this.content = source["content"];
	    }
	}
	export class DiffHunk {
	    header: string;
	    lines: DiffLine[];
	
	    static createFrom(source: any = {}) {
	        return new DiffHunk(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.header = source["header"];
	        this.lines = this.convertValues(source["lines"], DiffLine);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DiffFile {
	    path: string;
	    oldPath?: string;
	    status: string;
	    additions: number;
	    deletions: number;
	    hunks: DiffHunk[];
	
	    static createFrom(source: any = {}) {
	        return new DiffFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.oldPath = source["oldPath"];
	        this.status = source["status"];
	        this.additions = source["additions"];
	        this.deletions = source["deletions"];
	        this.hunks = this.convertValues(source["hunks"], DiffHunk);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Message {
	    id: string;
	    role: string;
	    content: string;
	    toolCalls?: ToolCall[];
	    toolCallId?: string;
	    createdAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.role = source["role"];
	        this.content = source["content"];
	        this.toolCalls = this.convertValues(source["toolCalls"], ToolCall);
	        this.toolCallId = source["toolCallId"];
	        this.createdAt = source["createdAt"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ToolCall {
	    id: string;
	    name: string;
	    args?: Record<string, any>;
	    status: string;
	    duration?: number;
	    result?: string;
	    files?: string[];
	
	    static createFrom(source: any = {}) {
	        return new ToolCall(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.args = source["args"];
	        this.status = source["status"];
	        this.duration = source["duration"];
	        this.result = source["result"];
	        this.files = source["files"];
	    }
	}
	export class ChatResult {
	    reply: string;
	    toolCalls?: ToolCall[];
	    messages?: Message[];
	    diff?: DiffFile[];
	    plan?: Plan;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new ChatResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.reply = source["reply"];
	        this.toolCalls = this.convertValues(source["toolCalls"], ToolCall);
	        this.messages = this.convertValues(source["messages"], Message);
	        this.diff = this.convertValues(source["diff"], DiffFile);
	        this.plan = this.convertValues(source["plan"], Plan);
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Conversation {
	    index: number;
	    query: string;
	    answer: string;
	    startTime: number;
	    endTime: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new Conversation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.query = source["query"];
	        this.answer = source["answer"];
	        this.startTime = source["startTime"];
	        this.endTime = source["endTime"];
	        this.error = source["error"];
	    }
	}
	
	
	
	export class DiffTurn {
	    turn: number;
	    label: string;
	    files: DiffFile[];
	    additions: number;
	    deletions: number;
	    createdAt: number;
	
	    static createFrom(source: any = {}) {
	        return new DiffTurn(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.turn = source["turn"];
	        this.label = source["label"];
	        this.files = this.convertValues(source["files"], DiffFile);
	        this.additions = source["additions"];
	        this.deletions = source["deletions"];
	        this.createdAt = source["createdAt"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Grant {
	    tool: string;
	    spec?: string;
	    isPrefix?: boolean;
	    createdAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Grant(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tool = source["tool"];
	        this.spec = source["spec"];
	        this.isPrefix = source["isPrefix"];
	        this.createdAt = source["createdAt"];
	    }
	}
	export class MCPToolMeta {
	    name: string;
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new MCPToolMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	    }
	}
	
	export class Model {
	    name: string;
	    alias: string;
	    modelId: string;
	    apiKey: string;
	    url: string;
	    protocol?: string;
	
	    static createFrom(source: any = {}) {
	        return new Model(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.alias = source["alias"];
	        this.modelId = source["modelId"];
	        this.apiKey = source["apiKey"];
	        this.url = source["url"];
	        this.protocol = source["protocol"];
	    }
	}
	export class RuleSource {
	    scope: string;
	    path: string;
	    exist: boolean;
	    mode?: string;
	    deny: number;
	    ask: number;
	    allow: number;
	
	    static createFrom(source: any = {}) {
	        return new RuleSource(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scope = source["scope"];
	        this.path = source["path"];
	        this.exist = source["exist"];
	        this.mode = source["mode"];
	        this.deny = source["deny"];
	        this.ask = source["ask"];
	        this.allow = source["allow"];
	    }
	}
	export class RuleView {
	    rule: string;
	    bucket: string;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new RuleView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rule = source["rule"];
	        this.bucket = source["bucket"];
	        this.source = source["source"];
	    }
	}
	export class PermissionState {
	    sessionId: string;
	    mode: string;
	    projectDir: string;
	    rules: RuleView[];
	    sources: RuleSource[];
	    grants: Grant[];
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new PermissionState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.mode = source["mode"];
	        this.projectDir = source["projectDir"];
	        this.rules = this.convertValues(source["rules"], RuleView);
	        this.sources = this.convertValues(source["sources"], RuleSource);
	        this.grants = this.convertValues(source["grants"], Grant);
	        this.warnings = source["warnings"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	
	export class Session {
	    id: string;
	    title: string;
	    project: string;
	    model: string;
	    permissionMode: string;
	    viewMode: string;
	    environment: string;
	    status: string;
	    startAt: number;
	    endAt: number;
	    messages: Message[];
	    conversations: Conversation[];
	    diffs?: DiffTurn[];
	    diffBaseline?: string;
	    diffTouched?: string[];
	    enabledTools?: string[];
	    enabledSkills?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Session(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.project = source["project"];
	        this.model = source["model"];
	        this.permissionMode = source["permissionMode"];
	        this.viewMode = source["viewMode"];
	        this.environment = source["environment"];
	        this.status = source["status"];
	        this.startAt = source["startAt"];
	        this.endAt = source["endAt"];
	        this.messages = this.convertValues(source["messages"], Message);
	        this.conversations = this.convertValues(source["conversations"], Conversation);
	        this.diffs = this.convertValues(source["diffs"], DiffTurn);
	        this.diffBaseline = source["diffBaseline"];
	        this.diffTouched = source["diffTouched"];
	        this.enabledTools = source["enabledTools"];
	        this.enabledSkills = source["enabledSkills"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SessionConfig {
	    title: string;
	    project: string;
	    model: string;
	    permissionMode: string;
	    viewMode: string;
	    environment: string;
	    enabledTools: string[];
	    enabledSkills: string[];
	
	    static createFrom(source: any = {}) {
	        return new SessionConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.project = source["project"];
	        this.model = source["model"];
	        this.permissionMode = source["permissionMode"];
	        this.viewMode = source["viewMode"];
	        this.environment = source["environment"];
	        this.enabledTools = source["enabledTools"];
	        this.enabledSkills = source["enabledSkills"];
	    }
	}
	export class SessionPatch {
	    title?: string;
	    model?: string;
	    permissionMode?: string;
	    viewMode?: string;
	    status?: string;
	    project?: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionPatch(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.model = source["model"];
	        this.permissionMode = source["permissionMode"];
	        this.viewMode = source["viewMode"];
	        this.status = source["status"];
	        this.project = source["project"];
	    }
	}
	export class SkillDetail {
	    id: string;
	    name: string;
	    description: string;
	    dir: string;
	    enabled: boolean;
	    alwaysInject: boolean;
	    builtin: boolean;
	    hasScripts: boolean;
	    error: string;
	    body: string;
	
	    static createFrom(source: any = {}) {
	        return new SkillDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.dir = source["dir"];
	        this.enabled = source["enabled"];
	        this.alwaysInject = source["alwaysInject"];
	        this.builtin = source["builtin"];
	        this.hasScripts = source["hasScripts"];
	        this.error = source["error"];
	        this.body = source["body"];
	    }
	}
	export class SkillMeta {
	    id: string;
	    name: string;
	    description: string;
	    dir: string;
	    enabled: boolean;
	    alwaysInject: boolean;
	    builtin: boolean;
	    hasScripts: boolean;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new SkillMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.dir = source["dir"];
	        this.enabled = source["enabled"];
	        this.alwaysInject = source["alwaysInject"];
	        this.builtin = source["builtin"];
	        this.hasScripts = source["hasScripts"];
	        this.error = source["error"];
	    }
	}
	
	export class ToolParamConfig {
	    name: string;
	    description: string;
	    required: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ToolParamConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.required = source["required"];
	    }
	}
	export class ToolConfig {
	    id: string;
	    name: string;
	    label: string;
	    description: string;
	    type: string;
	    icon: string;
	    enabled: boolean;
	    builtin: boolean;
	    parameters: ToolParamConfig[];
	    config: number[];
	    disabledTools: string[];
	    discovered: MCPToolMeta[];
	
	    static createFrom(source: any = {}) {
	        return new ToolConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.label = source["label"];
	        this.description = source["description"];
	        this.type = source["type"];
	        this.icon = source["icon"];
	        this.enabled = source["enabled"];
	        this.builtin = source["builtin"];
	        this.parameters = this.convertValues(source["parameters"], ToolParamConfig);
	        this.config = source["config"];
	        this.disabledTools = source["disabledTools"];
	        this.discovered = this.convertValues(source["discovered"], MCPToolMeta);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ToolFileChange {
	    time: number;
	    tool: string;
	    path: string;
	    rel: string;
	    action: string;
	    added: number;
	    removed: number;
	    files?: DiffFile[];
	
	    static createFrom(source: any = {}) {
	        return new ToolFileChange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.tool = source["tool"];
	        this.path = source["path"];
	        this.rel = source["rel"];
	        this.action = source["action"];
	        this.added = source["added"];
	        this.removed = source["removed"];
	        this.files = this.convertValues(source["files"], DiffFile);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ToolRuntimeStatus {
	    connected: boolean;
	    toolCount: number;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new ToolRuntimeStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connected = source["connected"];
	        this.toolCount = source["toolCount"];
	        this.error = source["error"];
	    }
	}
	export class ToolInfo {
	    id: string;
	    name: string;
	    label: string;
	    description: string;
	    type: string;
	    icon: string;
	    enabled: boolean;
	    builtin: boolean;
	    parameters: ToolParamConfig[];
	    config: number[];
	    disabledTools: string[];
	    discovered: MCPToolMeta[];
	    status: ToolRuntimeStatus;
	
	    static createFrom(source: any = {}) {
	        return new ToolInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.label = source["label"];
	        this.description = source["description"];
	        this.type = source["type"];
	        this.icon = source["icon"];
	        this.enabled = source["enabled"];
	        this.builtin = source["builtin"];
	        this.parameters = this.convertValues(source["parameters"], ToolParamConfig);
	        this.config = source["config"];
	        this.disabledTools = source["disabledTools"];
	        this.discovered = this.convertValues(source["discovered"], MCPToolMeta);
	        this.status = this.convertValues(source["status"], ToolRuntimeStatus);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	

}

