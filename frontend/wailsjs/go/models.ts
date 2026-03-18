export namespace main {
	
	export class ChainIssue {
	    index: number;
	    uuid: string;
	    type: string;
	    parent_uuid: string;
	    problem: string;
	
	    static createFrom(source: any = {}) {
	        return new ChainIssue(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.uuid = source["uuid"];
	        this.type = source["type"];
	        this.parent_uuid = source["parent_uuid"];
	        this.problem = source["problem"];
	    }
	}
	export class CompactMetadata {
	    trigger: string;
	    preTokens: number;
	
	    static createFrom(source: any = {}) {
	        return new CompactMetadata(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.trigger = source["trigger"];
	        this.preTokens = source["preTokens"];
	    }
	}
	export class CompactRuleReport {
	    entries_removed: number;
	    bytes_before: number;
	    bytes_after: number;
	    bytes_saved: number;
	    details?: string[];
	
	    static createFrom(source: any = {}) {
	        return new CompactRuleReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.entries_removed = source["entries_removed"];
	        this.bytes_before = source["bytes_before"];
	        this.bytes_after = source["bytes_after"];
	        this.bytes_saved = source["bytes_saved"];
	        this.details = source["details"];
	    }
	}
	export class CompactRuleResult {
	    name: string;
	    report: CompactRuleReport;
	
	    static createFrom(source: any = {}) {
	        return new CompactRuleResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.report = this.convertValues(source["report"], CompactRuleReport);
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
	export class CompactReport {
	    rules: CompactRuleResult[];
	    total_before: number;
	    total_after: number;
	    total_saved: number;
	
	    static createFrom(source: any = {}) {
	        return new CompactReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rules = this.convertValues(source["rules"], CompactRuleResult);
	        this.total_before = source["total_before"];
	        this.total_after = source["total_after"];
	        this.total_saved = source["total_saved"];
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
	
	
	export class CompactToImageResult {
	    html: string;
	    report: CompactReport;
	    new_session: string;
	
	    static createFrom(source: any = {}) {
	        return new CompactToImageResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.html = source["html"];
	        this.report = this.convertValues(source["report"], CompactReport);
	        this.new_session = source["new_session"];
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
	export class ContentSummary {
	    types: string[];
	    text_preview: string;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new ContentSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.types = source["types"];
	        this.text_preview = source["text_preview"];
	        this.size = source["size"];
	    }
	}
	export class Message {
	    uuid: string;
	    parentUuid: string;
	    type: string;
	    role: string;
	    timestamp: string;
	    isSidechain: boolean;
	    content_summary: ContentSummary;
	    is_tool_only: boolean;
	    is_system: boolean;
	    is_compact_boundary: boolean;
	    compact_meta?: CompactMetadata;
	    model: string;
	    raw: number[];
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.uuid = source["uuid"];
	        this.parentUuid = source["parentUuid"];
	        this.type = source["type"];
	        this.role = source["role"];
	        this.timestamp = source["timestamp"];
	        this.isSidechain = source["isSidechain"];
	        this.content_summary = this.convertValues(source["content_summary"], ContentSummary);
	        this.is_tool_only = source["is_tool_only"];
	        this.is_system = source["is_system"];
	        this.is_compact_boundary = source["is_compact_boundary"];
	        this.compact_meta = this.convertValues(source["compact_meta"], CompactMetadata);
	        this.model = source["model"];
	        this.raw = source["raw"];
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
	export class ConversationView {
	    messages: Message[];
	    total_size: number;
	    session_id: string;
	
	    static createFrom(source: any = {}) {
	        return new ConversationView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.messages = this.convertValues(source["messages"], Message);
	        this.total_size = source["total_size"];
	        this.session_id = source["session_id"];
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
	
	export class Project {
	    id: string;
	    name: string;
	    session_count: number;
	    mtime: number;
	
	    static createFrom(source: any = {}) {
	        return new Project(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.session_count = source["session_count"];
	        this.mtime = source["mtime"];
	    }
	}
	export class SaveRequest {
	    keep_uuids: string[];
	    deleted_uuids: string[];
	    insert_lines: string[];
	
	    static createFrom(source: any = {}) {
	        return new SaveRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.keep_uuids = source["keep_uuids"];
	        this.deleted_uuids = source["deleted_uuids"];
	        this.insert_lines = source["insert_lines"];
	    }
	}
	export class SaveResult {
	    success: boolean;
	    kept_lines: number;
	    new_size: number;
	    backup: string;
	
	    static createFrom(source: any = {}) {
	        return new SaveResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.kept_lines = source["kept_lines"];
	        this.new_size = source["new_size"];
	        this.backup = source["backup"];
	    }
	}
	export class Session {
	    id: string;
	    preview: string;
	    msg_count: number;
	    size: number;
	    mtime: number;
	
	    static createFrom(source: any = {}) {
	        return new Session(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.preview = source["preview"];
	        this.msg_count = source["msg_count"];
	        this.size = source["size"];
	        this.mtime = source["mtime"];
	    }
	}
	export class StartupArgs {
	    project: string;
	    session: string;
	
	    static createFrom(source: any = {}) {
	        return new StartupArgs(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.project = source["project"];
	        this.session = source["session"];
	    }
	}
	export class UpdateInfo {
	    current_version: string;
	    latest_version: string;
	    has_update: boolean;
	    download_url: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.current_version = source["current_version"];
	        this.latest_version = source["latest_version"];
	        this.has_update = source["has_update"];
	        this.download_url = source["download_url"];
	    }
	}

}

