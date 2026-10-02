/**
 * Badly indented on purpose, for `editor-formatting.spec.ts`: Re-Indent and
 * Format Document need something to fix. Picked by the `?path=` extension.
 */
export const MESSY_GO = `package main

import "fmt"

func messy(xs []int) int{
total := 0
      for _, x := range xs {
  if x > 0 {
total += x
        }
}
    return total
}

func main() {
	fmt.Println(messy([]int{1, 2}))
}
`;

export const MESSY_SWIFT = `import Foundation

final class Basket {
    private var items: [String] = []

    func add(_ item: String) {
  guard !item.isEmpty else {
return
      }
        items.append(item)
}

    func remove(_ item: String) {
        if let index = items.firstIndex(of: item) {
            items.remove(at: index)
        }
    }

    var count: Int {
        items.count
    }
}
`;
