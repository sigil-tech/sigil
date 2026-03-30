import { useRef, useState, useEffect, useCallback } from "preact/hooks";

interface VirtualListProps<T> {
  items: T[];
  rowHeight: number;
  renderItem: (item: T, index: number) => any;
  overscan?: number;
}

export function VirtualList<T>({
  items,
  rowHeight,
  renderItem,
  overscan = 5,
}: VirtualListProps<T>) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [scrollTop, setScrollTop] = useState(0);
  const [containerHeight, setContainerHeight] = useState(0);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    setContainerHeight(el.clientHeight);

    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        setContainerHeight(entry.contentRect.height);
      }
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const handleScroll = useCallback(() => {
    if (containerRef.current) {
      setScrollTop(containerRef.current.scrollTop);
    }
  }, []);

  const totalHeight = items.length * rowHeight;
  const startIndex = Math.max(0, Math.floor(scrollTop / rowHeight) - overscan);
  const endIndex = Math.min(
    items.length,
    Math.ceil((scrollTop + containerHeight) / rowHeight) + overscan
  );

  const visibleItems = items.slice(startIndex, endIndex);

  return (
    <div
      ref={containerRef}
      class="virtual-list"
      onScroll={handleScroll}
      style={{ overflow: "auto", height: "100%" }}
    >
      <div style={{ height: `${totalHeight}px`, position: "relative" }}>
        {visibleItems.map((item, i) => {
          const index = startIndex + i;
          return (
            <div
              key={index}
              style={{
                position: "absolute",
                top: `${index * rowHeight}px`,
                width: "100%",
                height: `${rowHeight}px`,
              }}
            >
              {renderItem(item, index)}
            </div>
          );
        })}
      </div>
    </div>
  );
}
