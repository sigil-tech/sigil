import { render, waitFor, fireEvent } from '@testing-library/preact';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { VMLauncher } from './VMLauncher';

declare const globalThis: any;

describe('VMLauncher', () => {
  const goMock = {
    VMStart: vi.fn(),
    VMStop: vi.fn(),
    VMStatus: vi.fn(),
    VMList: vi.fn(),
    VMMerge: vi.fn(),
  };

  beforeEach(() => {
    Object.values(goMock).forEach((fn: any) => fn.mockReset?.());
    globalThis.window = globalThis.window || {};
    (globalThis.window as any).go = { main: { App: goMock } };
    (globalThis.window as any).runtime = { EventsOn: () => () => {} };
  });

  afterEach(() => {
    delete (globalThis.window as any).go;
    delete (globalThis.window as any).runtime;
  });

  it('shows "No active session" when daemon reports none', async () => {
    goMock.VMStatus.mockResolvedValueOnce(null);
    goMock.VMList.mockResolvedValueOnce([]);
    const { getByText } = render(<VMLauncher />);
    await waitFor(() => {
      expect(getByText('No active session.')).toBeTruthy();
    });
    expect(getByText('Launch VM')).toBeTruthy();
  });

  it('renders an active session badge when VM is ready', async () => {
    goMock.VMStatus.mockResolvedValueOnce({
      id: 'session-abc-123',
      started_at: new Date().toISOString(),
      status: 'ready',
      merge_outcome: 'pending',
      disk_image_path: '/tmp/base.qcow2',
    });
    goMock.VMList.mockResolvedValueOnce([]);
    const { container } = render(<VMLauncher />);
    await waitFor(() => {
      expect(container.querySelector('.vm-badge--ready')).toBeTruthy();
    });
    expect(container.querySelector('.vm-session-id')?.textContent).toContain('session-abc');
  });

  it('disables Launch button when disk image path is empty', async () => {
    goMock.VMStatus.mockResolvedValueOnce(null);
    goMock.VMList.mockResolvedValueOnce([]);
    const { getByText } = render(<VMLauncher />);
    await waitFor(() => {
      const btn = getByText('Launch VM') as HTMLButtonElement;
      expect(btn.disabled).toBe(true);
    });
  });

  it('calls VMStop when the Stop button is clicked', async () => {
    goMock.VMStatus.mockResolvedValue({
      id: 'session-xyz-001',
      started_at: new Date().toISOString(),
      status: 'ready',
      merge_outcome: 'pending',
      disk_image_path: '/tmp/base.qcow2',
    });
    goMock.VMList.mockResolvedValue([]);
    goMock.VMStop.mockResolvedValue(undefined);
    const { getByText } = render(<VMLauncher />);
    await waitFor(() => {
      expect(getByText('Stop VM')).toBeTruthy();
    });
    fireEvent.click(getByText('Stop VM'));
    await waitFor(() => {
      expect(goMock.VMStop).toHaveBeenCalledWith('session-xyz-001');
    });
  });

  it('renders session history when VMList returns rows', async () => {
    goMock.VMStatus.mockResolvedValueOnce(null);
    goMock.VMList.mockResolvedValueOnce([
      {
        id: 'sess-hist-001',
        started_at: new Date('2026-04-15T10:00:00Z').toISOString(),
        ended_at: new Date('2026-04-15T10:30:00Z').toISOString(),
        status: 'stopped',
        merge_outcome: 'complete',
        disk_image_path: '/tmp/base.qcow2',
      },
    ]);
    const { container } = render(<VMLauncher />);
    await waitFor(() => {
      expect(container.querySelector('.vm-merge--complete')).toBeTruthy();
    });
  });
});
