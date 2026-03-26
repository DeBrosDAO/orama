export class SDKError extends Error {
  public readonly httpStatus: number;
  public readonly code: string;
  public readonly details: Record<string, any>;

  constructor(
    message: string,
    httpStatus: number = 500,
    code: string = "SDK_ERROR",
    details: Record<string, any> = {}
  ) {
    super(message);
    this.name = "SDKError";
    this.httpStatus = httpStatus;
    this.code = code;
    this.details = details;
  }

  static fromResponse(
    status: number,
    body: any,
    message?: string
  ): SDKError {
    const errorMsg = message || body?.error || `HTTP ${status}`;
    const code = body?.code || `HTTP_${status}`;
    return new SDKError(errorMsg, status, code, body);
  }

  toJSON() {
    return {
      name: this.name,
      message: this.message,
      httpStatus: this.httpStatus,
      code: this.code,
      details: this.details,
    };
  }
}
