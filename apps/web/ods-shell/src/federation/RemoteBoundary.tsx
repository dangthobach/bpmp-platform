import { Component, type ErrorInfo, type PropsWithChildren, type ReactNode } from "react";
import { ErrorPanel } from "@ods/shared-ui";

interface Props extends PropsWithChildren {
  remoteName: string;
  onError(error: unknown, phase: string): void;
}

interface State {
  error: Error | null;
  resetKey: number;
}

export class RemoteBoundary extends Component<Props, State> {
  state: State = { error: null, resetKey: 0 };

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    this.props.onError(error, `render:${info.componentStack ?? "unknown"}`);
  }

  render(): ReactNode {
    if (this.state.error) {
      return <ErrorPanel
        title={`${this.props.remoteName} is unavailable`}
        detail="The rest of ODS remains available. Retry this module or continue elsewhere."
        onRetry={() => this.setState((state) => ({ error: null, resetKey: state.resetKey + 1 }))}
      />;
    }
    return <div key={this.state.resetKey}>{this.props.children}</div>;
  }
}
