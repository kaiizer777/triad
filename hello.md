# Binary Search in Rust

Binary search is an efficient algorithm for finding an item in a sorted collection. It works by repeatedly dividing the search interval in half.

## Implementation

```rust
fn binary_search(arr: &[i32], target: i32) -> Option<usize> {
    let mut low = 0;
    let mut high = arr.len();

    while low < high {
        let mid = low + (high - low) / 2;

        match arr[mid].cmp(&target) {
            std::cmp::Ordering::Equal => return Some(mid),
            std::cmp::Ordering::Less => low = mid + 1,
            std::cmp::Ordering::Greater => high = mid,
        }
    }

    None
}

fn main() {
    let sorted_array = [1, 3, 5, 7, 9, 11, 13, 15, 17, 19];

    // Search for existing element
    match binary_search(&sorted_array, 7) {
        Some(index) => println!("Found 7 at index {}", index),
        None => println!("7 not found"),
    }

    // Search for non-existing element
    match binary_search(&sorted_array, 6) {
        Some(index) => println!("Found 6 at index {}", index),
        None => println!("6 not found"),
    }
}
```

## Complexity

- **Time Complexity**: O(log n)
- **Space Complexity**: O(1)
